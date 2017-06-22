package route

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/buoyantio/slow_cooker/load"
	restful "github.com/emicklei/go-restful"
)

type Response struct {
	error bool
	data  string
}

type SingleLoadRequest struct {
	Qps                  int
	Concurrency          int
	Method               string
	Interval             string
	Noreuse              bool
	Compress             bool
	ReportLatenciesCSV   string
	TotalRequests        int
	Headers              load.HeaderSet
	MetricAddr           string
	HashValue            uint64
	HashSampleRate       float64
	DstURL               string
	Hosts                string
	RequestData          []byte
	MetricsServerBackend string
	InfluxUsername       string
	InfluxPassword       string
	InfluxDatabase       string
	HistogramWindowSize  string
}

func (resp *Response) Error() string {
	return resp.data
}

var _running bool
var lock sync.Mutex

func isRunning() bool {
	lock.Lock()
	defer lock.Unlock()
	return _running
}

func startRunning() {
	lock.Lock()
	defer lock.Unlock()
	_running = true
}

func stopRunning() {
	lock.Lock()
	defer lock.Unlock()
	_running = false
}

// RunTest : Run test
func RunTest(request *restful.Request, response *restful.Response) {
	resp := &Response{}

	if !isRunning() {
		startRunning()
	} else {
		fmt.Println("Another job is running")
		resp.error = true
		resp.data = "Another job is running"
		response.WriteError(http.StatusBadRequest, resp)
		return
	}
	requestObj := SingleLoadRequest{}
	err := request.ReadEntity(&requestObj)
	if err != nil {
		fmt.Println(err)
		response.WriteError(http.StatusBadRequest, err)
		return
	}

	if requestObj.TotalRequests == 0 {
		fmt.Println("TotalRequests cannot be 0")
		resp.error = true
		resp.data = "TotalRequests cannot be 0"
		response.WriteError(http.StatusBadRequest, resp)
		return
	}

	singleLoad := load.SingleLoad{
		Qps:                  requestObj.Qps,
		Concurrency:          requestObj.Concurrency,
		Method:               requestObj.Method,
		Noreuse:              requestObj.Noreuse,
		Compress:             requestObj.Compress,
		TotalRequests:        uint64(requestObj.TotalRequests),
		NoLatencySummary:     true,
		Headers:              requestObj.Headers,
		Hosts:                strings.Split(requestObj.Hosts, ","),
		MetricAddr:           requestObj.MetricAddr,
		HashValue:            requestObj.HashValue,
		HashSampleRate:       requestObj.HashSampleRate,
		RequestData:          requestObj.RequestData,
		MetricsServerBackend: requestObj.MetricsServerBackend,
		InfluxUsername:       requestObj.InfluxUsername,
		InfluxPassword:       requestObj.InfluxPassword,
		InfluxDatabase:       requestObj.InfluxDatabase,
		ExitOnFinish:         false,
	}

	// parse interval
	if interval, err := time.ParseDuration(requestObj.Interval); err == nil {
		singleLoad.Interval = interval
	} else {
		response.WriteError(http.StatusBadRequest, err)
	}

	// parse DstURL
	if dstURL, err := url.Parse(requestObj.DstURL); err == nil {
		singleLoad.DstURL = *dstURL
	} else {
		response.WriteError(http.StatusBadRequest, err)
	}

	// parse histogramWindowSize
	if histogramWindowSize, err := time.ParseDuration(requestObj.HistogramWindowSize); err == nil {
		singleLoad.HistogramWindowSize = histogramWindowSize
	} else {
		response.WriteError(http.StatusBadRequest, err)
	}

	// parse metrics server backend
	if backend := requestObj.MetricsServerBackend; backend != "" {
		singleLoad.MetricsServerBackend = backend
	} else {
		singleLoad.MetricsServerBackend = "prometheus"
	}

	go func() {
		singleLoad.Run()
	}()

	go func() {
		for {
			select {
			case running := <-singleLoad.WatchStatus():
				if !running {
					stopRunning()
					log.Println("load job finished")
					break
				} else {

				}
			case output := <-singleLoad.WatchOutput():
				// TODO: Track this run so we can return status of this run
				log.Print(output)
				// response.WriteEntity(output)
			}
		}
	}()

	resp.error = false
	resp.data = "Load test is running now"
	if err := response.WriteEntity(resp); err != nil {
		response.WriteError(http.StatusInternalServerError, err)
	}
}
