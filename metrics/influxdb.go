package metrics

import (
	"fmt"
	"sync"
	"time"

	gomet "github.com/rcrowley/go-metrics"
	"github.com/vrischmann/go-metrics-influxdb"
)

// Influx :
type Influx struct {
	Counter                                map[string]gomet.Counter
	Histogram                              map[string]gomet.Histogram
	threadLock, counterLock, histogramLock *sync.Mutex
	running                                bool
}

// NewInflux :
func NewInflux(timeWindow time.Duration) *Influx {
	influx := Influx{}
	influx.threadLock, influx.counterLock, influx.histogramLock = new(sync.Mutex), new(sync.Mutex), new(sync.Mutex)
	counter := make(map[string]gomet.Counter)
	histogram := make(map[string]gomet.Histogram)
	counter[Requests] = gomet.NewCounter()
	gomet.Register(Requests, counter[Requests])
	counter[Successes] = gomet.NewCounter()
	gomet.Register(Successes, counter[Successes])
	// latencySample := NewSlidingWindowSample(600)
	latencySample := NewSlidingTimeWindowSample(timeWindow)
	histogram[LatencyHistogram] = gomet.NewHistogram(latencySample)
	gomet.Register(LatencyHistogram, histogram[LatencyHistogram])
	throughputSample := NewSlidingTimeWindowSample(timeWindow)
	histogram[ThroughputHistogram] = gomet.NewHistogram(throughputSample)
	gomet.Register(ThroughputHistogram, histogram[ThroughputHistogram])
	influx.Counter = counter
	influx.Histogram = histogram
	influx.running = false
	return &influx
}

// Monitor : implement Metrics
func (influx *Influx) Monitor(opts *ServerOpts) {
	influx.threadLock.Lock()
	if influx.running {
		fmt.Println("monitor has already running")
		return
	}
	influx.running = true
	influx.threadLock.Unlock()

	go func() {
		influxdb.InfluxDB(
			gomet.DefaultRegistry,
			opts.WriteInterval,
			opts.Host,
			opts.Database,
			opts.Username,
			opts.Password)

		defer func() {
			if r := recover(); r != nil {
				fmt.Println("error execution monitor routine: ", r)
			}
		}()
	}()
}

// CounterInc : implement Metrics
func (influx *Influx) CounterInc(name string) {
	influx.counterLock.Lock()
	defer influx.counterLock.Unlock()
	if counter, ok := influx.Counter[name]; ok {
		counter.Inc(1)
	}
}

// HistogramObserve : implement Metric
func (influx *Influx) HistogramObserve(name string, data float64) {
	influx.histogramLock.Lock()
	defer influx.histogramLock.Unlock()
	if histogram, ok := influx.Histogram[name]; ok {
		histogram.Update(int64(data))
	}
}
