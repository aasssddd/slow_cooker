package metrics

import "time"

const (
	// Requests : Counter
	Requests string = "requests"
	// Successes : Counter
	Successes string = "successes"
	// LatencyHistogram : Histogram
	LatencyHistogram string = "latency_ms"
)

// Metrics :
type Metrics interface {
	Monitor(opts *ServerOpts)
	CounterInc(name string)
	HistogramObserve(name string, data float64)
	Sync()
}

// ServerOpts :
type ServerOpts struct {
	Host, Username, Password, Database string
	WriteInterval                      time.Duration
}
