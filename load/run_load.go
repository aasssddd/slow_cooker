package load

import (
	"fmt"
	"hash/fnv"
	"log"
	"math"
	"math/rand"
	"net/http"
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
	sendTraffic          sync.WaitGroup
	ExitOnFinish         bool
	Output               chan string
	Running              chan bool
}

func (load *SingleLoad) Stop() {
	load.cleanup <- true
}

func (load *SingleLoad) pushOutput(output string) {
	if load.Output == nil {
		load.Output = make(chan string)
	}
	load.Output <- output
}

func (load *SingleLoad) setRunning(running bool) {
	if load.Running == nil {
		load.Running = make(chan bool, 1)
	}
	load.Running <- running
}

// Entrypoint
func (load *SingleLoad) Run() {
	// Repsonse tracking metadata.
	load.setRunning(true)
	handlerParam := NewHandlerParams(load)

	doTLS := load.DstURL.Scheme == "https"
	client := newClient(load.Compress, doTLS, load.Noreuse, load.Concurrency)
	// The time portion of the header can change due to timezone.
	timeLen := len(time.Now().Format(time.RFC3339))
	timePadding := strings.Repeat(" ", timeLen)
	intLen := len(fmt.Sprintf("%s", load.Interval))
	intPadding := strings.Repeat(" ", intLen-2)

	headerline := fmt.Sprintf("# sending %d %s req/s with concurrency=%d to %s ...\n", (load.Qps * load.Concurrency), load.Method, load.Concurrency, load.DstURL.String())
	fmt.Println(headerline)
	// load.pushOutput(headerline)
	headerline = fmt.Sprintf("# %s good/b/f t   goal%% %s min [p50 p95 p99  p999]  max bhash change\n", timePadding, intPadding)
	fmt.Println(headerline)
	// load.pushOutput(headerline)

	signal.Notify(handlerParam.interrupted, syscall.SIGINT)

	// Run Request
	RunRequest(load, client, handlerParam.timeToWait)

	// Collect Metrics
	collectMetrics(load, handlerParam)
}

// HandlerParams : Parameters for handle http response and timeout event
type HandlerParams struct {
	count              uint64
	size               uint64
	good               uint64
	bad                uint64
	failed             uint64
	min                int64
	max                int64
	failedHashCheck    int64
	hist               *hdrhistogram.Histogram
	globalHist         *hdrhistogram.Histogram
	latencyHistory     ring.IntRing
	received           chan *MeasuredResponse
	timeout            <-chan time.Time
	timeToWait         time.Duration
	totalTrafficTarget int
	cleanup            chan bool
	interrupted        chan os.Signal
}

// NewHandlerParams : initialize HandlerParams
func NewHandlerParams(params *SingleLoad) *HandlerParams {
	return &HandlerParams{
		count:              uint64(0),
		size:               uint64(0),
		good:               uint64(0),
		bad:                uint64(0),
		failed:             uint64(0),
		min:                int64(math.MaxInt64),
		max:                int64(0),
		failedHashCheck:    int64(0),
		hist:               hdrhistogram.New(0, DayInMs, 3),
		globalHist:         hdrhistogram.New(0, DayInMs, 3),
		latencyHistory:     ring.New(5),
		timeout:            time.After(params.Interval),
		received:           make(chan *MeasuredResponse),
		timeToWait:         CalcTimeToWait(&params.Qps),
		totalTrafficTarget: params.Qps * params.Concurrency * int(params.Interval.Seconds()),
		cleanup:            make(chan bool, 2),
		interrupted:        make(chan os.Signal, 2),
	}
}

// RunRequest : Parallel sending request with RunLoadParams.Concurrency threads
func RunRequest(params *SingleLoad, client *http.Client, timeToWait time.Duration) {
	StartSendingTraffic()
	for i := 0; i < params.Concurrency; i++ {
		ticker := time.NewTicker(timeToWait)
		go func() {
			// For each goroutine we want to reuse a buffer for performance reasons.
			bodyBuffer := make([]byte, 50000)
			params.sendTraffic.Add(1)
			for _ = range ticker.C {
				var checkHash bool
				hasher := fnv.New64a()
				if params.HashSampleRate > 0.0 {
					checkHash = ShouldCheckHash(params.HashSampleRate)
				} else {
					checkHash = false
				}
				shouldFinishLock.RLock()
				if !shouldFinish {
					shouldFinishLock.RUnlock()
					sendRequest(client, params.Method, &params.DstURL, params.Hosts[rand.Intn(len(params.Hosts))], params.Headers, params.RequestData, atomic.AddUint64(&reqID, 1), params.HashValue, checkHash, hasher, received, bodyBuffer)
				} else {
					shouldFinishLock.RUnlock()
					params.sendTraffic.Done()
					return
				}
			}
		}()
	}
}

func collectMetrics(params *SingleLoad, handlerParam *HandlerParams) {
	var metricsBackend metrics.Metrics

	switch strings.ToLower(params.MetricsServerBackend) {
	case ServerBackendPrometheus:
		metricsBackend = metrics.NewPrometheus()
	case ServerBackendInfluxDB:
		metricsBackend = metrics.NewInflux(params.HistogramWindowSize)
	}

	var opts metrics.ServerOpts
	opts = metrics.ServerOpts{
		Host:          params.MetricAddr,
		Username:      params.InfluxUsername,
		Password:      params.InfluxPassword,
		Database:      params.InfluxDatabase,
		WriteInterval: params.Interval,
	}
	metricsBackend.Monitor(&opts)

	for {
		select {
		// If we get a SIGINT, then start the shutdown process.
		case <-handlerParam.interrupted:
			handlerParam.cleanup <- true
		case <-handlerParam.cleanup:
			finishSendingTraffic()
			if !params.NoLatencySummary {
				hdrreport.PrintLatencySummary(handlerParam.globalHist)
			}

			if params.ReportLatenciesCSV != "" {
				err := hdrreport.WriteReportCSV(&params.ReportLatenciesCSV, handlerParam.globalHist)
				if err != nil {
					log.Panicf("Unable to write Latency CSV file: %v\n", err)
				}
			}
			go func() {
				// Don't Wait() in the event loop or else we'll block the workers
				// from draining.
				params.sendTraffic.Wait()
				fmt.Printf("Sleeping 5 seconds to ensure metrics are sent...\n")
				time.Sleep(5 * time.Second)
				params.setRunning(false)
				if params.ExitOnFinish {
					os.Exit(0)
				}
				metricsBackend.Stop()
				reqID = 0
			}()
			break
		case t := <-handlerParam.timeout:
			// When all requests are failures, ensure we don't accidentally
			// print out a monstrously huge number.
			if handlerParam.min == math.MaxInt64 {
				handlerParam.min = 0
			}
			// Periodically print stats about the request load.
			percentAchieved := int(math.Min((((float64(handlerParam.good) + float64(handlerParam.bad)) /
				float64(handlerParam.totalTrafficTarget)) * 100), 100))

			lastP99 := int(handlerParam.hist.ValueAtQuantile(99))
			// We want the change indicator to be based on
			// how far away the current value is from what
			// we've seen historically. This is why we call
			// CalculateChangeIndicator() first and then Push()
			changeIndicator := window.CalculateChangeIndicator(handlerParam.latencyHistory.Items, lastP99)
			handlerParam.latencyHistory.Push(lastP99)

			fmt.Printf("%s %6d/%1d/%1d %d %3d%% %s %3d [%3d %3d %3d %4d ] %4d %6d %s\n",
				t.Format(time.RFC3339),
				handlerParam.good,
				handlerParam.bad,
				handlerParam.failed,
				handlerParam.totalTrafficTarget,
				percentAchieved,
				params.Interval,
				handlerParam.min,
				handlerParam.hist.ValueAtQuantile(50),
				handlerParam.hist.ValueAtQuantile(95),
				handlerParam.hist.ValueAtQuantile(99),
				handlerParam.hist.ValueAtQuantile(999),
				handlerParam.max,
				handlerParam.failedHashCheck,
				changeIndicator)

			handlerParam.count = 0
			handlerParam.size = 0
			handlerParam.good = 0
			handlerParam.bad = 0
			handlerParam.min = math.MaxInt64
			handlerParam.max = 0
			handlerParam.failed = 0
			handlerParam.failedHashCheck = 0
			handlerParam.hist.Reset()
			handlerParam.timeout = time.After(params.Interval)

			if params.TotalRequests != 0 && reqID > params.TotalRequests {
				handlerParam.timeout = nil
				handlerParam.cleanup <- true
			}
		case managedResp := <-received:
			handlerParam.count++
			metricsBackend.CounterInc(metrics.Requests)
			if managedResp.err != nil {
				fmt.Fprintln(os.Stderr, managedResp.err)
				handlerParam.failed++
			} else {
				handlerParam.size += managedResp.sz
				if managedResp.failedHashCheck {
					handlerParam.failedHashCheck++
				}
				if managedResp.code >= 200 && managedResp.code < 500 {
					handlerParam.good++
					metricsBackend.CounterInc(metrics.Successes)
					metricsBackend.HistogramObserve(metrics.LatencyHistogram, float64(managedResp.latency))
				} else {
					handlerParam.bad++
				}

				if managedResp.latency < handlerParam.min {
					handlerParam.min = managedResp.latency
				}

				if managedResp.latency > handlerParam.max {
					handlerParam.max = managedResp.latency
				}
				metricsBackend.HistogramObserve(metrics.ThroughputHistogram, float64(handlerParam.good))
				handlerParam.hist.RecordValue(managedResp.latency)
				handlerParam.globalHist.RecordValue(managedResp.latency)
			}
		}
	}
}
