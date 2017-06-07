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
	counter := make(map[string]gomet.Counter)
	histrogram := make(map[string]gomet.Histogram)
	counter[Requests] = gomet.NewCounter()
	gomet.Register(Requests, counter[Requests])
	counter[Successes] = gomet.NewCounter()
	gomet.Register(Successes, counter[Successes])
	sample := gomet.NewExpDecaySample(1028, 0.015)
	histrogram[LatencyHistogram] = gomet.NewHistogram(sample)
	gomet.Register(LatencyHistogram, histrogram[LatencyHistogram])
	influx.Counter = counter
	influx.Histogram = histrogram
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
