package load

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
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
	PercentileMin int64
	Percentile50  int64
	Percentile95  int64
	Percentile99  int64
	PercentileMax int64
}

// HandlerParams : Parameters for handle http response and timeout event
type HandlerParams struct {
	requestData        []byte
	count              uint64
	size               uint64
	good               uint64
	bad                uint64
	failed             uint64
	min                int64
	max                int64
	failedHashCheck    int64
	hist               *hdrhistogram.Histogram
	GlobalHist         *hdrhistogram.Histogram
	latencyHistory     ring.IntRing
	received           chan *MeasuredResponse
	timeout            *time.Timer
	timeToWait         time.Duration
	totalTrafficTarget int
	cleanup            chan bool
	exit               chan bool
	interrupted        chan os.Signal
	shouldFinish       bool
	shouldFinishLock   sync.RWMutex
	sendTraffic        sync.WaitGroup
}

// AppLoad
type AppLoad struct {
	CommandMode         bool
	RunId               string        `json:"runId" binding:"required"`
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
	LoadTime            string        `json:'loadTime' binding:"required"`
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
		tasks = append(tasks, Task{UrlTemplate: load.DstURL, Method: load.Method, Data: load.Data})
	} else {
		tasks = load.Scenario
	}

	dstUrl, err := url.Parse(tasks[0].UrlTemplate)
	if err != nil {
		return errors.New("Unable to parse url: " + err.Error())
	}

	doTLS := dstUrl.Scheme == "https"
	client := newClient(load.Compress, doTLS, load.Noreuse, load.Concurrency)
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

	// Run Request
	load.runRequest(&tasks, client)

	// Collect Metrics
	load.collectMetrics()

	return nil
}

// NewHandlerParams : initialize HandlerParams
func NewHandlerParams(params *AppLoad) *HandlerParams {
	requestData := LoadData(params.Data)

	return &HandlerParams{
		requestData:        requestData,
		count:              uint64(0),
		size:               uint64(0),
		good:               uint64(0),
		bad:                uint64(0),
		failed:             uint64(0),
		min:                int64(math.MaxInt64),
		max:                int64(0),
		failedHashCheck:    int64(0),
		hist:               hdrhistogram.New(0, DayInMs, 3),
		GlobalHist:         hdrhistogram.New(0, DayInMs, 3),
		latencyHistory:     ring.New(5),
		timeout:            time.NewTimer(params.Interval),
		received:           make(chan *MeasuredResponse),
		timeToWait:         CalcTimeToWait(&params.Qps),
		totalTrafficTarget: params.Qps * params.Concurrency * int(params.Interval.Seconds()),
		cleanup:            make(chan bool, 2),
		exit:               make(chan bool, 2),
		interrupted:        make(chan os.Signal, 2),
	}
}

func CalcTimeToWait(qps *int) time.Duration {
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
			for _ = range ticker.C {
				var checkHash bool
				hasher := fnv.New64a()
				if load.HashSampleRate > 0.0 {
					checkHash = ShouldCheckHash(load.HashSampleRate)
				} else {
					checkHash = false
				}
				load.HandlerParams.shouldFinishLock.RLock()
				if !load.HandlerParams.shouldFinish {
					load.HandlerParams.shouldFinishLock.RUnlock() // compile path parameter

					drainResp := make(map[string]string)
					for _, task := range *tasks {
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
						resp := sendRequest(client, task.Method, dstUrl, load.Hosts[rand.Intn(len(load.Hosts))], load.Headers, LoadData(task.Data), atomic.AddUint64(&load.reqID, 1), load.HashValue, checkHash, hasher, load.HandlerParams.received, bodyBuffer)
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
			if load.HandlerParams.min == math.MaxInt64 {
				load.HandlerParams.min = 0
			}
			// Periodically print stats about the request load.
			percentAchieved := int(math.Min((((float64(load.HandlerParams.good) + float64(load.HandlerParams.bad)) /
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
				load.HandlerParams.good,
				load.HandlerParams.bad,
				load.HandlerParams.failed,
				load.HandlerParams.totalTrafficTarget,
				percentAchieved,
				load.Interval,
				load.HandlerParams.min,
				load.HandlerParams.hist.ValueAtQuantile(50),
				load.HandlerParams.hist.ValueAtQuantile(95),
				load.HandlerParams.hist.ValueAtQuantile(99),
				load.HandlerParams.hist.ValueAtQuantile(999),
				load.HandlerParams.max,
				load.HandlerParams.failedHashCheck,
				changeIndicator)

			load.HandlerParams.count = 0
			load.HandlerParams.size = 0
			load.HandlerParams.good = 0
			load.HandlerParams.bad = 0
			load.HandlerParams.min = math.MaxInt64
			load.HandlerParams.max = 0
			load.HandlerParams.failed = 0
			load.HandlerParams.failedHashCheck = 0
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
				load.HandlerParams.failed++
			} else {
				load.HandlerParams.size += managedResp.sz
				if managedResp.failedHashCheck {
					load.HandlerParams.failedHashCheck++
				}
				if managedResp.code >= 200 && managedResp.code < 500 {
					load.HandlerParams.good++
					metricsBackend.CounterInc(metrics.Successes)
					metricsBackend.HistogramObserve(metrics.LatencyHistogram, float64(managedResp.latency))
				} else {
					load.HandlerParams.bad++
				}

				if managedResp.latency < load.HandlerParams.min {
					load.HandlerParams.min = managedResp.latency
				}

				if managedResp.latency > load.HandlerParams.max {
					load.HandlerParams.max = managedResp.latency
				}
				metricsBackend.HistogramObserve(metrics.ThroughputHistogram, float64(load.HandlerParams.good))
				load.HandlerParams.hist.RecordValue(managedResp.latency)
				load.HandlerParams.GlobalHist.RecordValue(managedResp.latency)
			}
		}
	}
}
