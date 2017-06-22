package metrics

import (
	"log"
	"net"
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
	Halt             chan bool
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
	prom.Halt = make(chan bool, 1)

	return &prom
}

func (p *Prometheus) Stop() {
	p.Halt <- true
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

	var listener net.Listener
	if opts.Host != "" {
		if l, err := net.Listen("tcp", opts.Host); err != nil {
			log.Fatal(err)
		} else {
			listener = l
			go func() {
				http.Handle("/metrics", promhttp.Handler())
				http.Serve(listener, nil)
			}()
		}
	}
	go func() {
		for {
			select {
			case <-p.Halt:
				if listener != nil {
					listener.Close()
				}
				for _, v := range p.Counter {
					prometheus.Unregister(v)
				}
				for _, v := range p.Histogram {
					prometheus.Unregister(v)
				}
				break
			}
		}
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
