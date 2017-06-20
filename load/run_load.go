package load

import (
	"fmt"
	"hash/fnv"
	"log"
	"math"
	"math/rand"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/buoyantio/slow_cooker/hdrreport"
	"github.com/buoyantio/slow_cooker/metrics"
	"github.com/buoyantio/slow_cooker/ring"
	"github.com/buoyantio/slow_cooker/window"
	"github.com/codahale/hdrhistogram"
)

type SingleLoad struct {
	Qps                  int
	Concurrency          int
	Method               string
	Interval             time.Duration
	Noreuse              bool
	Compress             bool
	NoLatencySummary     bool
	ReportLatenciesCSV   string
	TotalRequests        uint64
	Headers              HeaderSet
	MetricAddr           string
	HashValue            uint64
	HashSampleRate       float64
	DstURL               url.URL
	Hosts                []string
	RequestData          []byte
	MetricsServerBackend string
	InfluxUsername       string
	InfluxPassword       string
	InfluxDatabase       string
	HistogramWindowSize  time.Duration
	cleanup              chan bool
}

func (load *SingleLoad) Stop() {
	load.cleanup <- true
}

func (load *SingleLoad) Run() {
	// Repsonse tracking metadata.
	count := uint64(0)
	size := uint64(0)
	good := uint64(0)
	bad := uint64(0)
	failed := uint64(0)
	min := int64(math.MaxInt64)
	max := int64(0)
	failedHashCheck := int64(0)
	hist := hdrhistogram.New(0, DayInMs, 3)
	globalHist := hdrhistogram.New(0, DayInMs, 3)
	latencyHistory := ring.New(5)
	received := make(chan *MeasuredResponse)
	timeout := time.After(load.Interval)
	timeToWait := CalcTimeToWait(&load.Qps)
	var totalTrafficTarget int
	totalTrafficTarget = load.Qps * load.Concurrency * int(load.Interval.Seconds())

	doTLS := load.DstURL.Scheme == "https"
	client := newClient(load.Compress, doTLS, load.Noreuse, load.Concurrency)
	var sendTraffic sync.WaitGroup
	// The time portion of the header can change due to timezone.
	timeLen := len(time.Now().Format(time.RFC3339))
	timePadding := strings.Repeat(" ", timeLen)
	intLen := len(fmt.Sprintf("%s", load.Interval))
	intPadding := strings.Repeat(" ", intLen-2)

	fmt.Printf("# sending %d %s req/s with concurrency=%d to %s ...\n", (load.Qps * load.Concurrency), load.Method, load.Concurrency, load.DstURL.String())
	fmt.Printf("# %s good/b/f t   goal%% %s min [p50 p95 p99  p999]  max bhash change\n", timePadding, intPadding)
	for i := 0; i < load.Concurrency; i++ {
		ticker := time.NewTicker(timeToWait)
		go func() {
			// For each goroutine we want to reuse a buffer for performance reasons.
			bodyBuffer := make([]byte, 50000)
			sendTraffic.Add(1)
			for _ = range ticker.C {
				var checkHash bool
				hasher := fnv.New64a()
				if load.HashSampleRate > 0.0 {
					checkHash = ShouldCheckHash(load.HashSampleRate)
				} else {
					checkHash = false
				}
				shouldFinishLock.RLock()
				if !shouldFinish {
					shouldFinishLock.RUnlock()
					sendRequest(client, load.Method, &load.DstURL, load.Hosts[rand.Intn(len(load.Hosts))], load.Headers, load.RequestData, atomic.AddUint64(&reqID, 1), load.HashValue, checkHash, hasher, received, bodyBuffer)
				} else {
					shouldFinishLock.RUnlock()
					sendTraffic.Done()
					return
				}
			}
		}()
	}

	load.cleanup = make(chan bool, 2)
	interrupted := make(chan os.Signal, 2)
	signal.Notify(interrupted, syscall.SIGINT)

	var metricsBackend metrics.Metrics

	switch strings.ToLower(load.MetricsServerBackend) {
	case ServerBackendPrometheus:
		metricsBackend = metrics.NewPrometheus()
	case ServerBackendInfluxDB:
		metricsBackend = metrics.NewInflux(load.HistogramWindowSize)
	}

	if load.MetricAddr != "" {
		var opts metrics.ServerOpts
		opts = metrics.ServerOpts{
			Host:          load.MetricAddr,
			Username:      load.InfluxUsername,
			Password:      load.InfluxPassword,
			Database:      load.InfluxDatabase,
			WriteInterval: load.Interval,
		}
		metricsBackend.Monitor(&opts)
	}

	for {
		select {
		// If we get a SIGINT, then start the shutdown process.
		case <-interrupted:
			load.cleanup <- true
		case <-load.cleanup:
			finishSendingTraffic()
			if !load.NoLatencySummary {
				hdrreport.PrintLatencySummary(globalHist)
			}

			if load.ReportLatenciesCSV != "" {
				err := hdrreport.WriteReportCSV(&load.ReportLatenciesCSV, globalHist)
				if err != nil {
					log.Panicf("Unable to write Latency CSV file: %v\n", err)
				}
			}
			go func() {
				// Don't Wait() in the event loop or else we'll block the workers
				// from draining.
				sendTraffic.Wait()
				os.Exit(0)
			}()
		case t := <-timeout:
			// When all requests are failures, ensure we don't accidentally
			// print out a monstrously huge number.
			if min == math.MaxInt64 {
				min = 0
			}
			// Periodically print stats about the request load.
			percentAchieved := int(math.Min((((float64(good) + float64(bad)) /
				float64(totalTrafficTarget)) * 100), 100))

			lastP99 := int(hist.ValueAtQuantile(99))
			// We want the change indicator to be based on
			// how far away the current value is from what
			// we've seen historically. This is why we call
			// CalculateChangeIndicator() first and then Push()
			changeIndicator := window.CalculateChangeIndicator(latencyHistory.Items, lastP99)
			latencyHistory.Push(lastP99)

			fmt.Printf("%s %6d/%1d/%1d %d %3d%% %s %3d [%3d %3d %3d %4d ] %4d %6d %s\n",
				t.Format(time.RFC3339),
				good,
				bad,
				failed,
				totalTrafficTarget,
				percentAchieved,
				load.Interval,
				min,
				hist.ValueAtQuantile(50),
				hist.ValueAtQuantile(95),
				hist.ValueAtQuantile(99),
				hist.ValueAtQuantile(999),
				max,
				failedHashCheck,
				changeIndicator)

			count = 0
			size = 0
			good = 0
			bad = 0
			min = math.MaxInt64
			max = 0
			failed = 0
			failedHashCheck = 0
			hist.Reset()
			timeout = time.After(load.Interval)

			if load.TotalRequests != 0 && reqID > load.TotalRequests {
				load.cleanup <- true
			}
		case managedResp := <-received:
			count++
			metricsBackend.CounterInc(metrics.Requests)
			if managedResp.err != nil {
				fmt.Fprintln(os.Stderr, managedResp.err)
				failed++
			} else {
				size += managedResp.sz
				if managedResp.failedHashCheck {
					failedHashCheck++
				}
				if managedResp.code >= 200 && managedResp.code < 500 {
					good++
					metricsBackend.CounterInc(metrics.Successes)
					metricsBackend.HistogramObserve(metrics.LatencyHistogram, float64(managedResp.latency))
				} else {
					bad++
				}

				if managedResp.latency < min {
					min = managedResp.latency
				}

				if managedResp.latency > max {
					max = managedResp.latency
				}
				metricsBackend.HistogramObserve(metrics.ThroughputHistogram, float64(good))
				hist.RecordValue(managedResp.latency)
				globalHist.RecordValue(managedResp.latency)
			}
		}
	}
}
