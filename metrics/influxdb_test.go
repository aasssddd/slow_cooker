package metrics

import (
	"fmt"
	"testing"
	"time"
)

func TestInfluxDB(t *testing.T) {
	influx := new(Influx).New()
	influx2 := new(Influx).New()

	// make sure only one goroutine is running
	opts := ServerOpts{
		Host:          "http://localhost:8086",
		Database:      "metrics",
		WriteInterval: time.Minute * 10,
	}
	fmt.Println("run monitor on first instance")
	influx.Monitor(&opts)
	// call twice will return fail if goroutine is still running
	fmt.Println("run twice on the same instance")
	influx.Monitor(&opts)
	// maybe it should works for multiple database assign
	fmt.Println("run monitor on different instance")

	influx2.Monitor(&opts)
	for i := 0; i < 5; i++ {
		influx2.CounterInc(Requests)
		influx2.HistogramObserve(LatencyHistogram, 1)
	}
	// default metrics will write every 10 Second, but we make it send immediately
	influx2.SendMetricsNow()

}
