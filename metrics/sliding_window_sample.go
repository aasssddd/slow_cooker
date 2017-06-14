package metrics

// go version of sliding window reservoir
// refer to : https://github.com/dropwizard/metrics/blob/3.2-development/metrics-core/src/main/java/com/codahale/metrics/SlidingWindowReservoir.java
// and : https://github.com/rcrowley/go-metrics/blob/master/sample.go
import (
	"sync"

	gomet "github.com/rcrowley/go-metrics"
)

// SlidingWindowSample : http://metrics.dropwizard.io/3.1.0/manual/core/#sliding-window-reservoirs
type SlidingWindowSample struct {
	count         int64
	mutex         sync.Mutex
	reservoirSize int
	values        []int64
}

// NewSlidingWindowSample : New
func NewSlidingWindowSample(reservoirSize int) gomet.Sample {
	if gomet.UseNilMetrics {
		return gomet.NilSample{}
	}
	return &SlidingWindowSample{
		reservoirSize: reservoirSize,
		values:        make([]int64, reservoirSize),
	}
}

// Count : implements interface gomet.Sample
func (s *SlidingWindowSample) Count() int64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.count
}

// Max : implements interface gomet.Sample
func (s *SlidingWindowSample) Max() int64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return gomet.SampleMax(s.values)
}

// Mean : implements interface gomet.Sample
func (s *SlidingWindowSample) Mean() float64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return gomet.SampleMean(s.values)
}

// Min : implements interface gomet.Sample
func (s *SlidingWindowSample) Min() int64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return gomet.SampleMin(s.values)
}

// Percentile : implements interface gomet.Sample
func (s *SlidingWindowSample) Percentile(ps float64) float64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return gomet.SamplePercentile(s.values, ps)
}

// Percentiles : implements interface gomet.Sample
func (s *SlidingWindowSample) Percentiles(ps []float64) []float64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return gomet.SamplePercentiles(s.values, ps)
}

// Size : implements interface gomet.Sample
func (s *SlidingWindowSample) Size() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return len(s.values)
}

// Snapshot : implements interface gomet.Sample
func (s *SlidingWindowSample) Snapshot() gomet.Sample {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	values := make([]int64, len(s.values))
	copy(values, s.values)
	return gomet.NewSampleSnapshot(s.count, values)
}

// StdDev : implements interface gomet.Sample
func (s *SlidingWindowSample) StdDev() float64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return gomet.SampleStdDev(s.values)
}

// Sum : implements interface gomet.Sample
func (s *SlidingWindowSample) Sum() int64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return gomet.SampleSum(s.values)
}

// Update : implements interface gomet.Sample
func (s *SlidingWindowSample) Update(value int64) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.count++
	s.values[int(s.count)%len(s.values)] = value
}

// Values : implements interface gomet.Sample
func (s *SlidingWindowSample) Values() []int64 {
	s.mutex.Lock()
	s.mutex.Unlock()
	values := make([]int64, len(s.values))
	copy(values, s.values)
	return values
}

// Variance : implements interface gomet.Sample
func (s *SlidingWindowSample) Variance() float64 {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return gomet.SampleVariance(s.values)
}

// Clear : implements interface gomet.Sample
func (s *SlidingWindowSample) Clear() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.count = 0
	s.values = make([]int64, s.reservoirSize)
}
