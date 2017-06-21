package load

import (
	"errors"
	"time"

	"github.com/golang/glog"
)

type LatencyQos struct {
	LatencyMs  int64
	Percentile int
}

type Record struct {
	Qps       int
	LatencyMs int64
}

// RunCalibrationParams : latency struct
type LatencyCalibration struct {
	LatencyQos       LatencyQos
	LoadTime         time.Duration
	InitialQps       int
	Step             int
	RunsPerIntensity int
	Load             AppLoad
	Results          []*Record
	FinalQps         int
}

func (load *LatencyCalibration) getSummaryLatency() int64 {
	var sum int64
	length := len(load.Results)
	for i := 0; i < load.RunsPerIntensity; i++ {
		sum += load.Results[length-1-i].LatencyMs
	}

	return sum / int64(load.RunsPerIntensity)

}

func (load *LatencyCalibration) Run() error {
	load.Results = make([]*Record, 0)
	qps := load.InitialQps
	for {
		load.Load.Qps = qps
		for i := 0; i < load.RunsPerIntensity; i++ {
			glog.Infof("Starting calibration run #%d with qps %d", i+1, qps)
			go func() {
				load.Load.Run()
			}()
			<-time.After(load.LoadTime)
			load.Load.Stop()
			latency := load.Load.HandlerParams.GlobalHist.ValueAtQuantile(float64(load.LatencyQos.Percentile))
			glog.Infof("Run #%d with qps %d has latency %f", i+1, qps, latency)
			load.Results = append(load.Results, &Record{
				LatencyMs: latency,
				Qps:       qps,
			})
		}

		latency := load.getSummaryLatency()

		if latency > load.LatencyQos.LatencyMs {
			if len(load.Results) <= load.RunsPerIntensity {
				return errors.New("Initial qps was unable to meet latency requirement")
			}

			load.FinalQps = load.Results[len(load.Results)-load.RunsPerIntensity-1].Qps
			glog.Infof("Found final Qps %d", load.FinalQps)
			return nil
		}

		qps += load.Step
	}

	return errors.New("Unreachable")
}
