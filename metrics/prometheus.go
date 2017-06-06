package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Prometheus :
type Prometheus struct {
	Addr             string
	Counter          map[string]prometheus.Counter
	Histogram        map[string]prometheus.Histogram
	LatencyHistogram prometheus.Histogram
}

// New :
func (p Prometheus) New() Prometheus {
	c := make(map[string]prometheus.Counter)
	c[Requests] = prometheus.NewCounter(prometheus.CounterOpts{
		Name: Requests,
		Help: "Number of requests",
	})
	c[Successes] = prometheus.NewCounter(prometheus.CounterOpts{
		Name: Successes,
		Help: "Number of successful requests",
	})
	h := make(map[string]prometheus.Histogram)
	h[LatencyHistogram] = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: LatencyHistogram,
		Help: "RPC latency distributions in milliseconds.",
		// 50 exponential buckets ranging from 0.5 ms to 3 minutes
		// TODO: make this tunable
		Buckets: prometheus.ExponentialBuckets(0.5, 1.3, 50),
	})
	p.Counter = c
	p.Histogram = h
	p.registerMetrics()

	return p
}

func (p Prometheus) registerMetrics() {
	for _, v := range p.Counter {
		prometheus.MustRegister(v)
	}

	for _, v := range p.Histogram {
		prometheus.MustRegister(v)
	}
}

// Monitor : implement Metrics interface
func (p Prometheus) Monitor(opts *ServerOpts) {
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		http.ListenAndServe(opts.Host, nil)
	}()
}

// CounterInc : implement metrics
func (p Prometheus) CounterInc(name string) {
	p.Counter[name].Inc()

}

// HistogramObserve : implement metrics
func (p Prometheus) HistogramObserve(name string, data float64) {
	p.Histogram[name].Observe(data)
}
