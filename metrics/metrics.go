package metrics

import (
	"strings"
	"time"
)

const (
	ServerBackendPrometheus string = "prometheus"
	ServerBackendInfluxDB   string = "influxdb"

	// Requests : Counter
	Requests string = "requests"
	// Successes : Counter
	Successes string = "successes"
	// LatencyHistogram : Histogram
	LatencyHistogram string = "latency_ms"
	// ThroughputHistogram : Histogram
	ThroughputHistogram string = "throughput"
)

type MetricsOpts struct {
	MetricsServerBackend string
	MetricAddr           string
	InfluxUsername       string
	InfluxPassword       string
	InfluxDatabase       string
}

// Metrics :
type Metrics interface {
	Monitor(opts *ServerOpts)
	CounterInc(name string)
	HistogramObserve(name string, data float64)
}

// ServerOpts :
type ServerOpts struct {
	Host, Username, Password, Database string
	WriteInterval                      time.Duration
}

func NewMetricsBackend(metricOpts *MetricsOpts, histogramWindowSize time.Duration, interval time.Duration) Metrics {
	if metricOpts == nil || metricOpts.MetricAddr == "" {
		return NoOpMetrics{}
	}

	var metricsBackend Metrics
	switch strings.ToLower(metricOpts.MetricsServerBackend) {
	case ServerBackendInfluxDB:
		metricsBackend = NewInflux(histogramWindowSize)
	default:
		metricsBackend = NewPrometheus()
	}

	opts := &ServerOpts{
		Host:          metricOpts.MetricAddr,
		Username:      metricOpts.InfluxUsername,
		Password:      metricOpts.InfluxPassword,
		Database:      metricOpts.InfluxDatabase,
		WriteInterval: interval,
	}
	metricsBackend.Monitor(opts)

	return metricsBackend
}

type NoOpMetrics struct{}

func (metrics NoOpMetrics) Monitor(opts *ServerOpts) {}

func (metrics NoOpMetrics) CounterInc(name string) {}

func (metrics NoOpMetrics) HistogramObserve(name string, data float64) {}
