package load

import (
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/buoyantio/slow_cooker/metrics"
	"github.com/stretchr/testify/assert"
)

var testData string
var err error

func TestRunLoad(t *testing.T) {
	// setup a local test server
	fmt.Println("starting test server ...")
	svr := http.Server{Addr: ":8080"}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello world\n")
	})
	go func() {
		svr.ListenAndServe()
	}()

	t.Run("Test data from stdin", func(t *testing.T) {
		t.Parallel()
		testData = "blahblah=123"
		err = run(testData)
		assert.Empty(t, err, "test load data from stdin: %v", err)
	})
	t.Run("Test data from file", func(t *testing.T) {
		t.Parallel()
		testData = "@test.txt"
		err = run(testData)
		assert.Empty(t, err, "test load file error: %v", err)
	})
	// data from stdin

	// tear down local test server
	defer svr.Shutdown(nil)
}

func run(data string) error {
	metricOpts := &metrics.MetricsOpts{}
	histogramWindowSize := time.Minute
	appLoad := AppLoad{
		CommandMode:         true,
		Qps:                 1,
		Concurrency:         2,
		Method:              "POST",
		Interval:            time.Second,
		Noreuse:             true,
		Compress:            false,
		NoLatencySummary:    false,
		ReportLatenciesCSV:  "",
		TotalRequests:       30,
		DstURL:              "http://localhost:8080",
		Hosts:               nil,
		Data:                data,
		Headers:             make(HeaderSet),
		MetricOpts:          metricOpts,
		HistogramWindowSize: histogramWindowSize,
	}
	return appLoad.Run()

}
