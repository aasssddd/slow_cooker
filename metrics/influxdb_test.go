package metrics

import (
	"testing"
	"time"
)

func TestInfluxDB(t *testing.T) {
	var duration, duration2 time.Duration
	duration, _ = time.ParseDuration("10s")
	duration2, _ = time.ParseDuration("5m")

	var influx Metrics = NewInflux(duration)
	var influx2 Metrics = NewInflux(duration2)

	// make sure only one goroutine is running
	opts := ServerOpts{
		Host:          "http://localhost:8086",
		Database:      "metrics",
		WriteInterval: time.Minute * 10,
	}
	influx.Monitor(&opts)
	// call twice will return fail if goroutine is still running
	influx.Monitor(&opts)
	// maybe it should works for multiple database assign
	influx2.Monitor(&opts)
	for i := 0; i < 5; i++ {
		influx2.CounterInc(Requests)
		influx2.HistogramObserve(LatencyHistogram, 1)
	}
	// default metrics will write every 10 Second, but we make it send immediately
	influx2.Sync()

}
