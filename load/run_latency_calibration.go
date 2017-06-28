package load

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang/glog"
)

const MAX_RUNS = 50

type SLO struct {
	LatencyMs  int64 `json:"latencyMs"`
	Percentile int   `json:"percentile"`
}

type CalibrationRecord struct {
	Concurrency int    `json:"concurrency"`
	LatencyMs   int64  `json:"latencyMs"`
	Failures    uint64 `json:"failures"`
}

type LatencyCalibrationRun struct {
	RunId       string
	Config      *LatencyCalibration
	Results     []*CalibrationRecord
	FinalResult *CalibrationRecord
}

func NewLatencyCalibrationRun(id string, calibrate *LatencyCalibration) *LatencyCalibrationRun {
	return &LatencyCalibrationRun{
		RunId:   id,
		Config:  calibrate,
		Results: make([]*CalibrationRecord, 0),
	}
}

// RunCalibrationParams : latency struct
type LatencyCalibration struct {
	SLO       SLO     `json:"slo"`
	Load      AppLoad `json:"appLoad"`
	Calibrate struct {
		InitialConcurrency int    `json:"initialConcurrency"`
		Step               int    `json:"step"`
		RunsPerIntensity   int    `json:"runsPerIntensity"`
		LoadTime           string `json:"loadTime"`
	} `json:"calibrate"`
}

func (load *LatencyCalibrationRun) getSummaryLatency() int64 {
	var sum int64
	length := len(load.Results)
	for i := 0; i < load.Config.Calibrate.RunsPerIntensity; i++ {
		sum += load.Results[length-1-i].LatencyMs
	}

	return sum / int64(load.Config.Calibrate.RunsPerIntensity)
}

func (load *LatencyCalibrationRun) Run() error {
	glog.Infof("Start running latency calibration for id: " + load.RunId)
	appLoad := &load.Config.Load
	// Override defaults for calibration
	appLoad.NoLatencySummary = true
	// Setting a long default interval as we don't need periodic metrics
	appLoad.Interval = time.Hour * 24
	loadDuration, err := time.ParseDuration(load.Config.Calibrate.LoadTime)
	if err != nil {
		glog.Errorf("Unable to parse load time '%s': ", load.Config.Calibrate.LoadTime, err.Error())
		return fmt.Errorf("Unable to parse load time %s: %s", load.Config.Calibrate.LoadTime, err.Error())
	}

	load.Results = make([]*CalibrationRecord, 0)
	concurrency := load.Config.Calibrate.InitialConcurrency
	runs := 1
	for runs < MAX_RUNS {
		appLoad.Concurrency = concurrency
		var failedCount uint64
		for i := 0; i < load.Config.Calibrate.RunsPerIntensity || failedCount > 0; i++ {
			failedCount = 0
			go func() {
				glog.Infof("Starting calibration run #%d with concurrency %d", i+1, concurrency)
				appLoad.Run()
			}()
			<-time.After(loadDuration)
			appLoad.Stop()

			if appLoad.HandlerParams.Failed+appLoad.HandlerParams.Bad > 0 {
				failedCount = appLoad.HandlerParams.Failed + appLoad.HandlerParams.Bad
			}

			latency := appLoad.HandlerParams.GlobalHist.ValueAtQuantile(float64(load.Config.SLO.Percentile))
			glog.Infof("Run #%d with concurrency %d finished with percentile %d, latency %d. failures %d",
				i+1, concurrency, load.Config.SLO.Percentile, latency, failedCount)
			load.Results = append(load.Results, &CalibrationRecord{
				LatencyMs:   latency,
				Concurrency: concurrency,
				Failures:    failedCount,
			})
		}

		if failedCount > 0 {
			glog.Warningf("Found %d failures in last run, ending calibration loop", failedCount)
			break
		}

		latency := load.getSummaryLatency()

		if latency > load.Config.SLO.LatencyMs {
			glog.Warningf("Last run's latency (%d) exceeds SLO (%d), ending calibration loop", latency, load.Config.SLO.LatencyMs)
			break
		}

		load.FinalResult = &CalibrationRecord{
			Concurrency: concurrency,
			LatencyMs:   latency,
			Failures:    0,
		}
		runs += 1
		concurrency += load.Config.Calibrate.Step
	}

	if load.FinalResult == nil {
		glog.Errorf("Initial Qps wasn't able to meet latency requirement for run id " + load.RunId)
		return errors.New("Initial qps was unable to meet latency requirement")
	}

	glog.Infof("Found final concurrency %d", load.FinalResult)
	return nil
}
