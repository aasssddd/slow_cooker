package metrics

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
}

// ServerOpts :
type ServerOpts struct {
	Host, Username, Password, Database string
}
