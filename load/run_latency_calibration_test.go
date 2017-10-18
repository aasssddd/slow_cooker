package load

import (
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/buoyantio/slow_cooker/metrics"
	"github.com/stretchr/testify/assert"
)

func TestRunLatencyCalibraion(t *testing.T) {
	// setup a local test server
	fmt.Println("starting test server ...")
	svr := http.Server{Addr: ":8080"}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello world\n")
		return
	})
	go func() {
		svr.ListenAndServe()
	}()

	t.Run("Test data from stdin", func(t *testing.T) {
		// t.Parallel()
		testData = "blahblah=123"
		err = RunCalibrate(testData)
		assert.Empty(t, err, "test load data from stdin: %v", err)
	})
	// t.Run("Test data from file", func(t *testing.T) {
	// 	// t.Parallel()
	// 	testData = "@test.txt"
	// 	err = RunCalibrate(testData)
	// 	assert.Empty(t, err, "test load file error: %v", err)
	// })
	// // data from remote url
	// t.Run("Test data from remote file", func(t *testing.T) {
	// 	// t.Parallel()
	// 	testData = "@https://s3.amazonaws.com/blahblah-files/test.txt"
	// 	err = RunCalibrate(testData)
	// 	assert.Empty(t, err, "test data from remote file：%v", err)
	// })
}

func RunCalibrate(data string) error {
	config := LatencyCalibration{
		SLO: SLO{
			LatencyMs:  10,
			Percentile: 99,
		},
		Calibrate: struct {
			InitialConcurrency int `json:"initialConcurrency"`
			Step               int `json:"step"`
			RunsPerIntensity   int `json:"runsPerIntensity"`
		}{
			InitialConcurrency: 1,
			Step:               1,
			RunsPerIntensity:   1,
		},
		LoadTime: "60s",
		Load: AppLoad{
			CommandMode:         false,
			Qps:                 1,
			Concurrency:         1,
			Method:              "POST",
			Interval:            time.Second,
			Noreuse:             true,
			Compress:            false,
			NoLatencySummary:    false,
			ReportLatenciesCSV:  "",
			DstURL:              "http://localhost:8080",
			Hosts:               nil,
			Data:                data,
			Headers:             make(HeaderSet),
			MetricOpts:          &metrics.MetricsOpts{},
			LoadTime:            "60s",
			HistogramWindowSize: time.Minute,
			Plan: RunningPlan{
				RunningSteps: []*RunningStep{
					&RunningStep{Qps: 10, Duration: "10s", Concurrency: 1},
					&RunningStep{Qps: 20, Duration: "20s", Concurrency: 2},
					&RunningStep{Qps: 30, Duration: "30s", Concurrency: 3},
				},
			},
		},
	}

	latencyCalibrationRun := LatencyCalibrationRun{
		Config: &config,
	}

	return latencyCalibrationRun.Run()
}
