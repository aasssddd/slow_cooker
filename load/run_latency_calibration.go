package load

import (
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/buoyantio/slow_cooker/ring"
	"github.com/buoyantio/slow_cooker/window"
	"github.com/codahale/hdrhistogram"
)

func (param *RunCalibrationParams) updateQPS(actual time.Duration) {
	var predict int
	currentQPS := param.Qps
	switch {
	case int(actual) == 0:
		// not running yet
		predict = int(1 / param.Qos.Latency.Seconds())

	case len(param.trainingData) == 0 && actual != 0:
		// only run once
		rand.Seed(time.Now().UnixNano())
		// change qos value randomly to identify boundary
		step := rand.Intn(1000)
		if actual > param.Qos.Latency {
			predict = int(1/param.Qos.Latency.Seconds()) - step
		} else {
			predict = int(1/param.Qos.Latency.Seconds()) + step
		}
		param.trainingData = append(param.trainingData, []float64{float64(currentQPS), actual.Seconds()})

	default:
		nextBoundary := 0
		if actual > param.Qos.Latency {
			// if actual greater than target, decrease Load
			for _, v := range param.trainingData {
				// find minimal boundary
				if qps := currentQPS - int(v[0]); qps > 0 {
					if (currentQPS - nextBoundary) > (currentQPS - qps) {
						nextBoundary = qps
					}
				}
			}
			// predict value is middle of [boundary..currentQPS]
			predict = currentQPS - rand.Intn(currentQPS-nextBoundary)
		} else if actual < param.Qos.Latency {
			// if actual less than target, increase load
			for _, v := range param.trainingData {
				if qps := int(v[0]) - currentQPS; qps > 0 {
					if (currentQPS - nextBoundary) > (currentQPS - qps) {
						nextBoundary = qps
					}
				}
			}
			if nextBoundary == 0 {
				// boundary not found, randomly create one
				predict = currentQPS + rand.Intn(500)
			} else {
				// predict value is middle of [currentQPS..boundary]
				predict = currentQPS + rand.Intn(nextBoundary-currentQPS)
			}
		} else {
			predict = currentQPS
		}
		param.trainingData = append(param.trainingData, []float64{float64(currentQPS), actual.Seconds()})
	}
	if predict < 0 {
		predict = 1
	}
	// feed current result into trainingData
	fmt.Println("actual latency: ", actual, " next QPS: ", predict)
	param.Qps = predict
}

var sendTraffic sync.WaitGroup
var received = make(chan *MeasuredResponse)

func round(params *RunCalibrationParams, hist *hdrhistogram.Histogram) {
	doTLS := params.DstURL.Scheme == "https"
	client := newClient(params.Compress, doTLS, params.Noreuse, params.Concurrency)
	qpsPerThread := params.Qps / params.Concurrency
	fmt.Println("qps per thread: ", qpsPerThread)
	for i := 0; i < params.Concurrency; i++ {
		go func() {
			timeToWait := CalcTimeToWait(&qpsPerThread)
			ticker := time.NewTicker(timeToWait)
			// For each goroutine we want to reuse a buffer for performance reasons.
			bodyBuffer := make([]byte, 50000)
			sendTraffic.Add(1)
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
					sendTraffic.Done()
					return
				}
			}
		}()
	}
}

func (params *RunCalibrationParams) Run() {
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

	timeout := time.After(params.Interval)
	var totalTrafficTarget int
	totalTrafficTarget = params.Qps * params.Concurrency * int(params.Interval.Seconds())

	// The time portion of the header can change due to timezone.
	timeLen := len(time.Now().Format(time.RFC3339))
	timePadding := strings.Repeat(" ", timeLen)
	intLen := len(fmt.Sprintf("%s", params.Interval))
	intPadding := strings.Repeat(" ", intLen-2)

	fmt.Printf("# sending %d %s req/s with concurrency=%d to %s ...\n", (params.Qps * params.Concurrency), params.Method, params.Concurrency, params.DstURL.String())
	fmt.Printf("# %s good/b/f t   goal%% %s min [p50 p95 p99  p999]  max bhash change\n", timePadding, intPadding)
	params.updateQPS(0)
	round(params, hist)

	cleanup := make(chan bool, 2)
	interrupted := make(chan os.Signal, 2)
	newRound := make(chan bool, 1)
	signal.Notify(interrupted, syscall.SIGINT)

	for {
		select {
		// If we get a SIGINT, then start the shutdown process.
		case <-interrupted:
			cleanup <- true
		case <-cleanup:
			finishSendingTraffic()
			go func() {
				// Don't Wait() in the event loop or else we'll block the workers
				// from draining.
				sendTraffic.Wait()
				os.Exit(0)
			}()
		case <-newRound:
			hist.Reset()
			timeout = time.After(params.Interval)
			StartSendingTraffic()
			round(params, hist)
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
				params.Interval,
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
			// finish current round
			finishSendingTraffic()

			// set confidence value to 3 times
			confidence := params.Qos.ConfidenceTimes
			threshold := params.Qos.TolerencePrecentage
			maxAllow := params.Qos.Latency.Seconds() * (1 + threshold)
			minAllow := params.Qos.Latency.Seconds() * (1 - threshold)
			actual := time.Duration(float64(hist.ValueAtQuantile(99)) * 1000000).Seconds()
			fmt.Printf("allow range is %f > %f > %f ", maxAllow, actual, minAllow)
			if actual > minAllow && maxAllow > actual {
				confidence++
			} else {
				confidence = 0
			}

			if confidence >= 2 {
				fmt.Println("QPS is approximate to ", params.Qps)
				cleanup <- true
			} else {
				// update QPS
				actualDuration := time.Duration(hist.ValueAtQuantile(99) * time.Millisecond.Nanoseconds())
				params.updateQPS(actualDuration)
				go func() {
					sendTraffic.Wait()
					newRound <- true
				}()
			}

		case managedResp := <-received:
			count++
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
				} else {
					bad++
				}

				if managedResp.latency < min {
					min = managedResp.latency
				}

				if managedResp.latency > max {
					max = managedResp.latency
				}
				hist.RecordValue(managedResp.latency)
				globalHist.RecordValue(managedResp.latency)
			}
		}
	}
}
