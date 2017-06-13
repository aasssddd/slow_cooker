package metrics

// go version of sliding window reservoir
// refer to : https://github.com/dropwizard/metrics/blob/3.2-development/metrics-core/src/main/java/com/codahale/metrics/SlidingTimeWindowReservoir.java
// and : https://github.com/rcrowley/go-metrics/blob/master/sample.go
import (
	"sync"
	"time"

	gomet "github.com/rcrowley/go-metrics"
)

const (
	collisionBuffer int64 = 256
	trimThreshold   int64 = 256
	clearBuffer     int64 = int64(time.Hour/time.Nanosecond) * 256
)

// SlidingTimeWindowSample : http://metrics.dropwizard.io/3.1.0/manual/core/#sliding--time-window-reservoirs
type SlidingTimeWindowSample struct {
	window   int64
	lastTick int64
	count    int64
	mutex    sync.Mutex
	values   map[int64]int64
}

// NewSlidingTimeWindowSample : New
func NewSlidingTimeWindowSample(duration time.Duration) gomet.Sample {
	if gomet.UseNilMetrics {
		return gomet.NilSample{}
	}
	sample := &SlidingTimeWindowSample{
		values:   make(map[int64]int64),
		lastTick: time.Now().UnixNano() * collisionBuffer,
		window:   duration.Nanoseconds() * collisionBuffer,
	}
	return sample
}

func (s *SlidingTimeWindowSample) trim() {
	now := s.getTick()
	windowStart := now - s.window
	windowEnd := now + clearBuffer
	if windowStart < windowEnd {
		for k := range s.values {
			if k < windowStart {
				delete(s.values, k)
			}
			if k >= windowEnd {
				delete(s.values, k)
			}
		}
	} else {
		for k := range s.values {
			if windowEnd < k && k < windowStart {
				delete(s.values, k)
			}
		}
	}
}

func (s *SlidingTimeWindowSample) getTick() int64 {
	oldTick := s.lastTick
	tick := time.Now().UnixNano()
	if tick-oldTick > 0 {
		s.lastTick = tick
	} else {
		s.lastTick = oldTick + 1
	}
	return s.lastTick
}

// Count : implements interface gomet.Sample
func (s *SlidingTimeWindowSample) Count() int64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.count
}

// Max : implements interface gomet.Sample
func (s *SlidingTimeWindowSample) Max() int64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return gomet.SampleMax(s.mappedValue())
}

func (s *SlidingTimeWindowSample) mappedValue() []int64 {
	values := make([]int64, len(s.values))
	for _, v := range s.values {
		values = append(values, v)
	}
	return values
}

// Mean : implements interface gomet.Sample
func (s *SlidingTimeWindowSample) Mean() float64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return gomet.SampleMean(s.mappedValue())
}

// Min : implements interface gomet.Sample
func (s *SlidingTimeWindowSample) Min() int64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return gomet.SampleMin(s.mappedValue())
}

// Percentile : implements interface gomet.Sample
func (s *SlidingTimeWindowSample) Percentile(ps float64) float64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return gomet.SamplePercentile(s.mappedValue(), ps)
}

// Percentiles : implements interface gomet.Sample
func (s *SlidingTimeWindowSample) Percentiles(ps []float64) []float64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return gomet.SamplePercentiles(s.mappedValue(), ps)
}

// Size : implements interface gomet.Sample
func (s *SlidingTimeWindowSample) Size() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return len(s.values)
}

// Snapshot : implements interface gomet.Sample
func (s *SlidingTimeWindowSample) Snapshot() gomet.Sample {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return gomet.NewSampleSnapshot(s.count, s.mappedValue())
}

// StdDev : implements interface gomet.Sample
func (s *SlidingTimeWindowSample) StdDev() float64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return gomet.SampleStdDev(s.mappedValue())
}

// Sum : implements interface gomet.Sample
func (s *SlidingTimeWindowSample) Sum() int64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return gomet.SampleSum(s.mappedValue())
}

// Update : implements interface gomet.Sample
func (s *SlidingTimeWindowSample) Update(value int64) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.count++
	if s.count%trimThreshold == 0 {
		s.trim()
	}
	s.values[s.getTick()] = value
}

// Values : implements interface gomet.Sample
func (s *SlidingTimeWindowSample) Values() []int64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.mappedValue()
}

// Variance : implements interface gomet.Sample
func (s *SlidingTimeWindowSample) Variance() float64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return gomet.SampleVariance(s.mappedValue())
}

// Clear : implements interface gomet.Sample
func (s *SlidingTimeWindowSample) Clear() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.count = 0
	s.values = make(map[int64]int64)
}
