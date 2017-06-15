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

// Sync : Implements Metrics interface
func (p Prometheus) Sync() {

}

// NewPrometheus :
func NewPrometheus() *Prometheus {
	prom := Prometheus{}
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
	prom.Counter = c
	prom.Histogram = h
	prom.registerMetrics()

	return &prom
}

func (p *Prometheus) registerMetrics() {
	for _, v := range p.Counter {
		prometheus.MustRegister(v)
	}

	for _, v := range p.Histogram {
		prometheus.MustRegister(v)
	}
}

// Monitor : implement Metrics interface
func (p *Prometheus) Monitor(opts *ServerOpts) {
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		http.ListenAndServe(opts.Host, nil)
	}()
}

// CounterInc : implement metrics
func (p *Prometheus) CounterInc(name string) {
	if counter, ok := p.Counter[name]; ok {
		counter.Inc()
	}
}

// HistogramObserve : implement metrics
func (p *Prometheus) HistogramObserve(name string, data float64) {
	if histogram, ok := p.Histogram[name]; ok {
		histogram.Observe(data)
	}
}
