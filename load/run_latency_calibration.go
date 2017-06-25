package load

import (
	"errors"
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

func (load *LatencyCalibration) Run() (int, error) {
	loadDuration, err := time.ParseDuration(load.Load.LoadTime)
	if err != nil {
		return 0, errors.New("Unable to parse load time: " + err.Error())
	}

	load.Results = make([]*CalibrationRecord, 0)
	concurrency := load.InitialConcurrency
	finalConcurrency := 0
	runs := 1
	for {
		load.Load.Concurrency = concurrency
		for i := 0; i < load.RunsPerIntensity; i++ {
			glog.Infof("Starting calibration run #%d with concurrency %d", i+1, concurrency)
			go func() {
				load.Load.Run()
			}()
			<-time.After(loadDuration)
			load.Load.Stop()
			latency := load.Load.HandlerParams.GlobalHist.ValueAtQuantile(float64(load.SLO.Percentile))
			glog.Infof("Run #%d with concurrency %d has latency %d", i+1, concurrency, latency)
			load.Results = append(load.Results, &CalibrationRecord{
				LatencyMs:   latency,
				Concurrency: concurrency,
			})
		}

		latency := load.getSummaryLatency()

		if latency > load.SLO.LatencyMs {
			if len(load.Results) <= load.RunsPerIntensity {
				return 0, errors.New("Initial qps was unable to meet latency requirement")
			}

			finalConcurrency = load.Results[len(load.Results)-load.RunsPerIntensity-1].Concurrency
			glog.Infof("Found final concurrency %d", finalConcurrency)
			break
		}

		runs += 1

		if runs > MAX_RUNS {
			return 0, errors.New("Max runs reached without finding final qps")
		}

		concurrency += load.Step
	}

	return finalConcurrency, nil
}
