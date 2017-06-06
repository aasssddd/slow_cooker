package metrics

import (
	"time"

	gomet "github.com/rcrowley/go-metrics"
	"github.com/vrischmann/go-metrics-influxdb"
)

// Influx :
type Influx struct {
	Addr, Username, Password string
	Counter                  map[string]gomet.Counter
	Histogram                map[string]gomet.Histogram
}

// New :
func (influx Influx) New() Influx {
	c := make(map[string]gomet.Counter)
	h := make(map[string]gomet.Histogram)
	c[Requests] = gomet.NewCounter()
	gomet.Register(Requests, c[Requests])
	c[Successes] = gomet.NewCounter()
	gomet.Register(Successes, c[Successes])
	s := gomet.NewExpDecaySample(1028, 0.015)
	h[LatencyHistogram] = gomet.NewHistogram(s)
	gomet.Register(LatencyHistogram, h[LatencyHistogram])
	influx.Counter = c
	influx.Histogram = h
	return influx
}

// Monitor : implement Metrics
func (influx Influx) Monitor(opts *ServerOpts) {
	go influxdb.InfluxDB(
		gomet.DefaultRegistry,
		time.Second*5,
		opts.Host,
		opts.Database,
		opts.Username,
		opts.Password)
}

// CounterInc : implement Metrics
func (influx Influx) CounterInc(name string) {
	influx.Counter[name].Inc(1)
}

// HistogramObserve : implement Metric
func (influx Influx) HistogramObserve(name string, data float64) {
	influx.Histogram[name].Update(int64(data))
}
