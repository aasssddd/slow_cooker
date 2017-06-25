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
	Concurrency int
	LatencyMs   int64
}

// RunCalibrationParams : latency struct
type LatencyCalibration struct {
	SLO                SLO     `json:"slo"`
	InitialConcurrency int     `json:"initialConcurrency"`
	Step               int     `json:"step"`
	RunsPerIntensity   int     `json:"runsPerIntensity"`
	Load               AppLoad `json:"appLoad"`
	LoadTime           string  `json:"loadTime"`
	RunId              string  `json:"runId"`

	// internal state
	Results []*CalibrationRecord
}

func (load *LatencyCalibration) getSummaryLatency() int64 {
	var sum int64
	length := len(load.Results)
	for i := 0; i < load.RunsPerIntensity; i++ {
		sum += load.Results[length-1-i].LatencyMs
	}

	return sum / int64(load.RunsPerIntensity)
}

func (load *LatencyCalibration) getFinalConcurrency() (int, error) {
	if len(load.Results) <= load.RunsPerIntensity {
		glog.Errorf("Initial Qps wasn't able to meet latency requirement for run id " + load.RunId)
		return 0, errors.New("Initial qps was unable to meet latency requirement")
	}

	finalConcurrency := load.Results[len(load.Results)-load.RunsPerIntensity-1].Concurrency
	glog.Infof("Found final concurrency %d", finalConcurrency)
	return finalConcurrency, nil
}

func (load *LatencyCalibration) Run() (int, error) {
	glog.Infof("Start running latency calibration for id: " + load.RunId)
	// Override defaults for calibration
	load.Load.NoLatencySummary = true
	// Setting a long default interval as we don't need periodic metrics
	load.Load.Interval = time.Hour * 24
	loadDuration, err := time.ParseDuration(load.LoadTime)
	if err != nil {
		glog.Errorf("Unable to parse load time %s: ", load.LoadTime, err.Error())
		return 0, fmt.Errorf("Unable to parse load time %s: %s", load.LoadTime, err.Error())
	}
	load.Results = make([]*CalibrationRecord, 0)
	concurrency := load.InitialConcurrency
	finalConcurrency := 0
	runs := 1
	for {
		load.Load.Concurrency = concurrency
		var failedCount uint64
		for i := 0; i < load.RunsPerIntensity; i++ {
			failedCount = 0
			go func() {
				glog.Infof("Starting calibration run #%d with concurrency %d", i+1, concurrency)
				load.Load.Run()
			}()
			<-time.After(loadDuration)
			load.Load.Stop()
			if load.Load.HandlerParams.failed > 0 {
				failedCount = load.Load.HandlerParams.failed
				break
			}
			latency := load.Load.HandlerParams.GlobalHist.ValueAtQuantile(float64(load.SLO.Percentile))
			glog.Infof("Run #%d with concurrency %d finished with percentile %d, latency %d",
				i+1, concurrency, load.SLO.Percentile, latency)
			load.Results = append(load.Results, &CalibrationRecord{
				LatencyMs:   latency,
				Concurrency: concurrency,
			})
		}

		if failedCount > 0 {
			glog.Warningf("Found %d failures in last run, ending calibration loop", failedCount)
			return load.getFinalConcurrency()
		}

		latency := load.getSummaryLatency()

		if latency > load.SLO.LatencyMs {
			return load.getFinalConcurrency()
		}

		runs += 1

		if runs > MAX_RUNS {
			glog.Errorf("Max runs reached without finding final qps for " + load.RunId)
			return 0, errors.New("Max runs reached without finding final qps")
		}

		concurrency += load.Step
	}

	return finalConcurrency, nil
}
