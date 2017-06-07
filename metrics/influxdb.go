package metrics

import (
	"fmt"
	"sync"
	"time"

	gomet "github.com/rcrowley/go-metrics"
	"github.com/vrischmann/go-metrics-influxdb"
)

const (
	funcTimeout time.Duration = 1 * time.Second
)

// Influx :
type Influx struct {
	Counter                                map[string]gomet.Counter
	Histogram                              map[string]gomet.Histogram
	threadLock, counterLock, histogramLock *sync.Mutex
	running                                *bool
}

// New :
func (influx Influx) New() Influx {
	influx.threadLock, influx.counterLock, influx.histogramLock = new(sync.Mutex), new(sync.Mutex), new(sync.Mutex)
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
	status := false
	influx.running = &status
	return influx
}

// Monitor : implement Metrics
func (influx Influx) Monitor(opts *ServerOpts) {
	if *influx.running {
		fmt.Println("monitor has already running")
		return
	}
	*influx.running = true
	influx.threadLock.Lock()
	chRelease := make(chan bool, 1)

	go func() {
		influxdb.InfluxDB(
			gomet.DefaultRegistry,
			time.Second*1,
			opts.Host,
			opts.Database,
			opts.Username,
			opts.Password)

		defer func() {
			if r := recover(); r != nil {
				fmt.Println("error execution monitor routine: ", r)
				chRelease <- true
			}
		}()
	}()
	go func(influx *Influx) {
		for {
			switch {
			case <-chRelease:
				influx.threadLock.Unlock()
				*influx.running = false
			default:
			}
		}
	}(&influx)

}

// CounterInc : implement Metrics
func (influx Influx) CounterInc(name string) {
	influx.counterLock.Lock()
	influx.Counter[name].Inc(1)
	influx.counterLock.Unlock()
}

// HistogramObserve : implement Metric
func (influx Influx) HistogramObserve(name string, data float64) {
	influx.histogramLock.Lock()
	influx.Histogram[name].Update(int64(data))
	influx.histogramLock.Unlock()
}
