package load

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"hash"
	"hash/fnv"
	"io"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"os/signal"
	"strconv"
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

type HeaderSet map[string]string

type BenchmarkRecord struct {
	Failures      uint64 `json:"failures"`
	PercentileMin int64  `json:"percentileMin"`
	Percentile50  int64  `json:"percentile50"`
	Percentile95  int64  `json:"percentile95"`
	Percentile99  int64  `json:"percentile99"`
	PercentileMax int64  `json:"percentileMax"`
}

type BenchmarkRequest struct {
	RunId            string  `json:"runId"`
	LoadTime         string  `json:"loadTime"`
	AppLoad          AppLoad `json:"appLoad"`
	RunsPerIntensity int     `json:"runsPerIntensity"`
}

// HandlerParams : Parameters for handle http response and timeout event
type HandlerParams struct {
	// Public available counters
	Good            uint64
	Bad             uint64
	Failed          uint64
	Min             int64
	Max             int64
	FailedHashCheck int64
	GlobalHist      *hdrhistogram.Histogram

	count              uint64
	size               uint64
	timeout            *time.Timer
	timeToWait         time.Duration
	totalTrafficTarget int
	requestData        [][]byte
	hist               *hdrhistogram.Histogram
	latencyHistory     ring.IntRing
	received           chan *MeasuredResponse
	cleanup            chan bool
	exit               chan bool
	interrupted        chan os.Signal
	shouldFinish       bool
	shouldFinishLock   sync.RWMutex
	sendTraffic        sync.WaitGroup
	startStep          chan *RunningStep
	stepLock           sync.RWMutex
	stopStep           bool
	stopStepSignal     chan bool
}

type RunningStep struct {
	Qps         int    `json:"qps"`
	Concurrency int    `json:concurrency`
	Duration    string `json:"string"`
}

type RunningPlan struct {
	RunningSteps []*RunningStep `json:"runningSteps"`
}

// AppLoad
type AppLoad struct {
	CommandMode         bool
	Plan                RunningPlan   `json:"plan"`
	Qps                 int           `json:"qps"`
	Concurrency         int           `json:"concurrency"`
	Method              string        `json:"method"`
	Interval            time.Duration `json:"interval"`
	Noreuse             bool          `json:"noreuse"`
	Compress            bool          `json:"compress"`
	NoLatencySummary    bool          `json:"noLatencySummary"`
	ReportLatenciesCSV  string        `json:"reportLatenciesCSV"`
	TotalRequests       uint64        `json:"totalRequests"`
	HashValue           uint64        `json:"hashValue"`
	HashSampleRate      float64       `json:"hashSampleRate"`
	DstURL              string        `json:"url"`
	Hosts               []string      `json:"hosts"`
	Data                string        `json:"data"`
	LoadTime            string        `json:"loadTime" binding:"required"`
	Scenario            []Task        `json:"scenario"`
	Headers             HeaderSet     `json:"headers"`
	HistogramWindowSize time.Duration
	reqID               uint64
	HandlerParams       *HandlerParams
	MetricOpts          *metrics.MetricsOpts
}

type Task struct {
	UrlTemplate string `json:"url_template"`
	Method      string `json:"method"`
	Data        string `json:"data"`
	DrainResp   string `json:"drain_resp"`
}

func (load *AppLoad) onExit() {
	if load.CommandMode {
		os.Exit(0)
	}

	load.HandlerParams.timeout.Stop()
}

func (load *AppLoad) Stop() {
	load.HandlerParams.cleanup <- true
	load.HandlerParams.sendTraffic.Wait()
}

// Entrypoint
func (load *AppLoad) Run() error {
	// Repsonse tracking metadata.
	load.HandlerParams = NewHandlerParams(load)
	if len(load.Hosts) == 0 {
		load.Hosts = []string{""}
	}

	var tasks []Task

	if len(load.Scenario) == 0 {
		tasks = make([]Task, 1)
		tasks[0] = Task{UrlTemplate: load.DstURL, Method: load.Method, Data: load.Data}
	} else {
		tasks = load.Scenario
	}

	// The time portion of the header can change due to timezone.
	timeLen := len(time.Now().Format(time.RFC3339))
	timePadding := strings.Repeat(" ", timeLen)
	intLen := len(fmt.Sprintf("%s", load.Interval))
	intPadding := strings.Repeat(" ", intLen-2)

	fmt.Printf("# sending %d %s req/s with concurrency=%d to %s ...\n", (load.Qps * load.Concurrency), load.Method, load.Concurrency, load.DstURL)
	fmt.Printf("# %s good/b/f t   goal%% %s min [p50 p95 p99  p999]  max bhash change\n", timePadding, intPadding)

	if load.CommandMode {
		signal.Notify(load.HandlerParams.interrupted, syscall.SIGINT)
	}
	var steps []*RunningStep
	if len(load.Plan.RunningSteps) > 0 {
		steps = load.Plan.RunningSteps
	} else {
		steps = append(steps, &RunningStep{
			Qps:         load.Qps,
			Concurrency: load.Concurrency,
			Duration:    load.LoadTime,
		})
	}
	// get QPS, Concurrency, LoadTime from Plan

	// start runner process
	load.StartLoadRunner()

	// push jobs to runner
	for _, step := range steps {
		load.HandlerParams.startStep <- step
	}

	// Collect Metrics (handler)
	load.collectMetrics()

	return nil
}

func (load *AppLoad) StartLoadRunner() {
	go func() {
		var tasks []Task

		if len(load.Scenario) == 0 {
			tasks = make([]Task, 1)
			tasks[0] = Task{UrlTemplate: load.DstURL, Method: load.Method, Data: load.Data}
		} else {
			tasks = load.Scenario
		}

		dstURL, err := url.Parse(tasks[0].UrlTemplate)
		if err != nil {
			log.Panicf("Unable to parse url: %v", err.Error())
		} else {
			doTLS := dstURL.Scheme == "https"

			for {
				select {
				case step := <-load.HandlerParams.startStep:
					load.HandlerParams.stepLock.Lock()
					load.HandlerParams.stopStep = false
					fmt.Printf("start step with Qps: %v Concurrency: %v duration: %v\n", step.Qps, step.Concurrency, step.Duration)
					load.Qps = step.Qps
					load.Concurrency = step.Concurrency
					load.HandlerParams.timeToWait = CalcTimeToWait(&load.Qps)

					client := newClient(load.Compress, doTLS, load.Noreuse, load.Concurrency)
					loadTime, err := time.ParseDuration(step.Duration)
					if err != nil {
						log.Panicf("Unable to parse duration, %v", step.Duration)
						load.HandlerParams.stopStepSignal <- true
					} else {
						// Run Request
						load.runRequest(&tasks, client)
					}

					// event listener
					go func() {
						for {
							select {
							case <-time.After(loadTime):
								load.HandlerParams.stopStepSignal <- true

							case <-load.HandlerParams.stopStepSignal:
								load.HandlerParams.stopStep = true
								load.HandlerParams.stepLock.Unlock()
								return
							}
						}
					}()

				}

			}
		}
	}()
}

// NewHandlerParams : initialize HandlerParams
func NewHandlerParams(params *AppLoad) *HandlerParams {

	return &HandlerParams{
		Good:               uint64(0),
		Bad:                uint64(0),
		Failed:             uint64(0),
		Min:                int64(math.MaxInt64),
		Max:                int64(0),
		FailedHashCheck:    int64(0),
		GlobalHist:         hdrhistogram.New(0, DayInMs, 3),
		count:              uint64(0),
		size:               uint64(0),
		hist:               hdrhistogram.New(0, DayInMs, 3),
		latencyHistory:     ring.New(5),
		timeout:            time.NewTimer(params.Interval),
		received:           make(chan *MeasuredResponse),
		timeToWait:         CalcTimeToWait(&params.Qps),
		totalTrafficTarget: params.Qps * params.Concurrency * int(params.Interval.Seconds()),
		cleanup:            make(chan bool, 2),
		exit:               make(chan bool, 2),
		startStep:          make(chan *RunningStep),
		stopStep:           true,
		stopStepSignal:     make(chan bool, 2),
		interrupted:        make(chan os.Signal, 2),
	}
}

func CalcTimeToWait(qps *int) time.Duration {
	if *qps == 0 {
		return time.Duration(time.Nanosecond * 1)
	}

	return time.Duration(int(time.Second) / *qps)
}

func (h *HeaderSet) String() string {
	return ""
}

func (h *HeaderSet) Set(s string) error {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) < 2 || len(parts[0]) == 0 {
		return fmt.Errorf("Header invalid")
	}
	name := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])
	(*h)[name] = value
	return nil
}

// Sample Rate is between [0.0, 1.0] and determines what percentage of request bodies
// should be checked that their hash matches a known hash.
func ShouldCheckHash(sampleRate float64) bool {
	return rand.Float64() < sampleRate
}

func newClient(
	compress bool,
	https bool,
	noreuse bool,
	maxConn int) *http.Client {
	tr := http.Transport{
		DisableCompression:  !compress,
		DisableKeepAlives:   noreuse,
		MaxIdleConnsPerHost: maxConn,
		Proxy:               http.ProxyFromEnvironment,
	}
	if https {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &http.Client{Transport: &tr}
}

func sendRequest(
	client *http.Client,
	method string,
	url *url.URL,
	host string,
	headers HeaderSet,
	requestData []byte,
	reqID uint64,
	hashValue uint64,
	checkHash bool,
	hasher hash.Hash64,
	received chan *MeasuredResponse,
	bodyBuffer []byte,
) []byte {
	req, err := http.NewRequest(method, url.String(), bytes.NewBuffer(requestData))
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		fmt.Fprintf(os.Stderr, "\n")
	}
	if host != "" {
		req.Host = host
	}
	req.Header.Add("Sc-Req-Id", strconv.FormatUint(reqID, 10))
	for k, v := range headers {
		req.Header.Add(k, v)
	}

	var elapsed time.Duration
	start := time.Now()

	trace := &httptrace.ClientTrace{
		GotFirstResponseByte: func() {
			elapsed = time.Since(start)
		},
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	response, err := client.Do(req)
	if err != nil {
		received <- &MeasuredResponse{err: err}
	} else {
		defer response.Body.Close()

		if !checkHash {
			buf := bytes.NewBuffer(nil)
			if sz, err := io.CopyBuffer(buf, response.Body, bodyBuffer); err == nil {
				received <- &MeasuredResponse{
					sz:      uint64(sz),
					code:    response.StatusCode,
					latency: elapsed.Nanoseconds() / 1000000}
				return buf.Bytes()
			} else {
				received <- &MeasuredResponse{err: err}
			}
		} else {
			if bytes, err := ioutil.ReadAll(response.Body); err != nil {
				received <- &MeasuredResponse{err: err}
			} else {
				hasher.Write(bytes)
				sum := hasher.Sum64()
				failedHashCheck := false
				if hashValue != sum {
					failedHashCheck = true
				}
				received <- &MeasuredResponse{
					sz:              uint64(len(bytes)),
					code:            response.StatusCode,
					latency:         elapsed.Nanoseconds() / 1000000,
					failedHashCheck: failedHashCheck}

				return bytes
			}
		}
	}
	return nil
}

// RunRequest : Parallel sending request with RunLoadParams.Concurrency threads
func (load *AppLoad) runRequest(tasks *[]Task, client *http.Client) {
	for i := 0; i < load.Concurrency; i++ {
		ticker := time.NewTicker(load.HandlerParams.timeToWait)
		go func() {
			// For each goroutine we want to reuse a buffer for performance reasons.
			bodyBuffer := make([]byte, 50000)
			load.HandlerParams.sendTraffic.Add(1)

			var dataIndex []*struct {
				Index int
				Data  [][]byte
			}
			for _, task := range *tasks {
				dataIndex = append(dataIndex, &struct {
					Index int
					Data  [][]byte
				}{
					0,
					LoadData(task.Data),
				})
			}
			for _ = range ticker.C {
				for _, data := range dataIndex {
					if data.Index > len(data.Data)-1 {
						// start over from begining
						data.Index = 0
					}
				}

				var checkHash bool
				hasher := fnv.New64a()
				if load.HashSampleRate > 0.0 {
					checkHash = ShouldCheckHash(load.HashSampleRate)
				} else {
					checkHash = false
				}

				load.HandlerParams.shouldFinishLock.RLock()
				if !(load.HandlerParams.shouldFinish && load.HandlerParams.stopStep) {
					load.HandlerParams.shouldFinishLock.RUnlock() // compile path parameter

					drainResp := make(map[string]string)
					for i, task := range *tasks {

						var dstUrl *url.URL
						var err error
						parsedUrl := task.UrlTemplate
						for k, v := range drainResp {
							parsedUrl = strings.Replace(task.UrlTemplate, ":"+k, v, -1)
						}

						dstUrl, err = url.Parse(parsedUrl)

						if err != nil {
							log.Panicf("URL parsing error")
						}
						resp := sendRequest(client, task.Method, dstUrl, load.Hosts[rand.Intn(len(load.Hosts))], load.Headers, dataIndex[i].Data[dataIndex[i].Index], atomic.AddUint64(&load.reqID, 1), load.HashValue, checkHash, hasher, load.HandlerParams.received, bodyBuffer)
						dataIndex[i].Index = dataIndex[i].Index + 1

						if task.DrainResp != "" {
							if len(resp) > 0 {
								data := map[string]interface{}{}
								json.Unmarshal(resp, &data)
								drainResp[task.DrainResp] = data[task.DrainResp].(string)
							}
						}
					}
				} else {
					load.HandlerParams.shouldFinishLock.RUnlock()
					load.HandlerParams.sendTraffic.Done()
					return
				}
			}
		}()
	}
}

func (load *AppLoad) collectMetrics() {
	metricsBackend := metrics.NewMetricsBackend(load.MetricOpts, load.HistogramWindowSize, load.Interval)

	for {
		select {
		case <-load.HandlerParams.exit:
			log.Println("Exiting load event loop..")
			load.onExit()
			return
		// If we get a SIGINT, then start the shutdown process.
		case <-load.HandlerParams.interrupted:
			load.HandlerParams.cleanup <- true
		case <-load.HandlerParams.cleanup:
			load.HandlerParams.shouldFinishLock.Lock()
			load.HandlerParams.shouldFinish = true
			load.HandlerParams.shouldFinishLock.Unlock()

			if !load.NoLatencySummary {
				hdrreport.PrintLatencySummary(load.HandlerParams.GlobalHist)
			}

			if load.ReportLatenciesCSV != "" {
				err := hdrreport.WriteReportCSV(&load.ReportLatenciesCSV, load.HandlerParams.GlobalHist)
				if err != nil {
					log.Panicf("Unable to write Latency CSV file: %v\n", err)
				}
			}
			go func() {
				// Don't Wait() in the event loop or else we'll block the workers
				// from draining.
				load.HandlerParams.sendTraffic.Wait()
				load.HandlerParams.exit <- true
			}()
		case t := <-load.HandlerParams.timeout.C:
			// When all requests are failures, ensure we don't accidentally
			// print out a monstrously huge number.
			if load.HandlerParams.Min == math.MaxInt64 {
				load.HandlerParams.Min = 0
			}
			// Periodically print stats about the request load.
			percentAchieved := int(math.Min((((float64(load.HandlerParams.Good) + float64(load.HandlerParams.Bad)) /
				float64(load.HandlerParams.totalTrafficTarget)) * 100), 100))

			lastP99 := int(load.HandlerParams.hist.ValueAtQuantile(99))
			// We want the change indicator to be based on
			// how far away the current value is from what
			// we've seen historically. This is why we call
			// CalculateChangeIndicator() first and then Push()
			changeIndicator := window.CalculateChangeIndicator(load.HandlerParams.latencyHistory.Items, lastP99)
			load.HandlerParams.latencyHistory.Push(lastP99)

			fmt.Printf("%s %6d/%1d/%1d %d %3d%% %s %3d [%3d %3d %3d %4d ] %4d %6d %s\n",
				t.Format(time.RFC3339),
				load.HandlerParams.Good,
				load.HandlerParams.Bad,
				load.HandlerParams.Failed,
				load.HandlerParams.totalTrafficTarget,
				percentAchieved,
				load.Interval,
				load.HandlerParams.Min,
				load.HandlerParams.hist.ValueAtQuantile(50),
				load.HandlerParams.hist.ValueAtQuantile(95),
				load.HandlerParams.hist.ValueAtQuantile(99),
				load.HandlerParams.hist.ValueAtQuantile(999),
				load.HandlerParams.Max,
				load.HandlerParams.FailedHashCheck,
				changeIndicator)

			load.HandlerParams.count = 0
			load.HandlerParams.size = 0
			load.HandlerParams.Good = 0
			load.HandlerParams.Bad = 0
			load.HandlerParams.Min = math.MaxInt64
			load.HandlerParams.Max = 0
			load.HandlerParams.Failed = 0
			load.HandlerParams.FailedHashCheck = 0
			load.HandlerParams.hist.Reset()
			load.HandlerParams.timeout = time.NewTimer(load.Interval)

			if load.TotalRequests != 0 && load.reqID > load.TotalRequests {
				load.HandlerParams.cleanup <- true
			}
		case managedResp := <-load.HandlerParams.received:
			load.HandlerParams.count++
			metricsBackend.CounterInc(metrics.Requests)
			if managedResp.err != nil {
				fmt.Fprintln(os.Stderr, managedResp.err)
				load.HandlerParams.Failed++
			} else {
				load.HandlerParams.size += managedResp.sz
				if managedResp.failedHashCheck {
					load.HandlerParams.FailedHashCheck++
				}
				if managedResp.code >= 200 && managedResp.code < 500 {
					load.HandlerParams.Good++
					metricsBackend.CounterInc(metrics.Successes)
					metricsBackend.HistogramObserve(metrics.LatencyHistogram, float64(managedResp.latency))
				} else {
					load.HandlerParams.Bad++
				}

				if managedResp.latency < load.HandlerParams.Min {
					load.HandlerParams.Min = managedResp.latency
				}

				if managedResp.latency > load.HandlerParams.Max {
					load.HandlerParams.Max = managedResp.latency
				}
				metricsBackend.HistogramObserve(metrics.ThroughputHistogram, float64(load.HandlerParams.Good))
				load.HandlerParams.hist.RecordValue(managedResp.latency)
				load.HandlerParams.GlobalHist.RecordValue(managedResp.latency)
			}
		}
	}
}
