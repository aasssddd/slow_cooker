package main

import (
	"flag"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/buoyantio/slow_cooker/load"
	"github.com/buoyantio/slow_cooker/metrics"
)

const (
	modeServer string = "server"
)

// DayInMs 1 day in milliseconds
const DayInMs int64 = 24 * 60 * 60 * 1000000

// MeasuredResponse holds metadata about the response
// we receive from the server under test.
type MeasuredResponse struct {
	sz              uint64
	code            int
	latency         int64
	timeout         bool
	failedHashCheck bool
	err             error
}

func ExUsage(msg string, args ...interface{}) {
	fmt.Fprintln(os.Stderr, fmt.Sprintf(msg, args...))
	fmt.Fprintln(os.Stderr, "Try --help for help.")
	os.Exit(64)
}

func main() {
	qps := flag.Int("qps", 1, "QPS to send to backends per request thread")
	concurrency := flag.Int("concurrency", 1, "Number of request threads")
	host := flag.String("host", "", "value of Host header to set")
	method := flag.String("method", "GET", "HTTP method to use")
	interval := flag.Duration("interval", 10*time.Second, "reporting interval")
	noreuse := flag.Bool("noreuse", false, "don't reuse connections")
	compress := flag.Bool("compress", false, "use compression")
	noLatencySummary := flag.Bool("noLatencySummary", false, "suppress the final latency summary")
	reportLatenciesCSV := flag.String("reportLatenciesCSV", "",
		"filename to output hdrhistogram latencies in CSV")
	help := flag.Bool("help", false, "show help message")
	totalRequests := flag.Uint64("totalRequests", 0, "total number of requests to send before exiting")
	headers := make(load.HeaderSet)
	flag.Var(&headers, "header", "HTTP request header. (can be repeated.)")
	data := flag.String("data", "", "HTTP request data")
	metricAddr := flag.String("metric-addr", "", "address to serve metrics on")
	metricsServerBackend := flag.String("metric-server-backend", "", "value can be promethus or influxdb")
	influxUsername := flag.String("influx-username", "", "influxdb username")
	influxPassword := flag.String("influx-password", "", "influxdb password")
	influxDatabase := flag.String("influx-database", "metrics", "influxdb database")
	hashValue := flag.Uint64("hashValue", 0, "fnv-1a hash value to check the request body against")
	hashSampleRate := flag.Float64("hashSampleRate", 0.0, "Sampe Rate for checking request body's hash. Interval in the range of [0.0, 1.0]")
	histogramWindowSize := flag.Duration("histrogram-window-size", time.Minute, "Slide window size of histogram, default is 1 minute")
	serverPort := flag.Int("server-port", 8081, "Define server should runing on which port, default is 8081")
	mode := flag.String("mode", "load", "Select running mode form load/latency/throughput/server")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s <url> [flags]\n", path.Base(os.Args[0]))
		flag.PrintDefaults()
	}

	flag.Parse()

	if *help {
		flag.Usage()
		os.Exit(64)
	}

	if *mode == modeServer {
		server := NewServer(*serverPort)
		server.Run()
	}

	if flag.NArg() != 1 {
		ExUsage("Expecting one argument: the target url to test, e.g. http://localhost:4140/")
	}

	dstURL := flag.Arg(0)

	if *qps < 1 {
		ExUsage("qps must be at least 1")
	}

	if *concurrency < 1 {
		ExUsage("concurrency must be at least 1")
	}

	hosts := strings.Split(*host, ",")
	fmt.Println("%+v", hosts)

	metricOpts := &metrics.MetricsOpts{
		MetricsServerBackend: *metricsServerBackend,
		MetricAddr:           *metricAddr,
		InfluxUsername:       *influxUsername,
		InfluxPassword:       *influxPassword,
		InfluxDatabase:       *influxDatabase,
	}

	run := load.AppLoad{
		CommandMode:         true,
		Qps:                 *qps,
		Concurrency:         *concurrency,
		Method:              *method,
		Interval:            *interval,
		Noreuse:             *noreuse,
		Compress:            *compress,
		NoLatencySummary:    *noLatencySummary,
		ReportLatenciesCSV:  *reportLatenciesCSV,
		TotalRequests:       *totalRequests,
		HashValue:           *hashValue,
		HashSampleRate:      *hashSampleRate,
		DstURL:              dstURL,
		Headers:             headers,
		Hosts:               hosts,
		Data:                *data,
		MetricOpts:          metricOpts,
		HistogramWindowSize: *histogramWindowSize,
	}

	if err := run.Run(); err != nil {
		fmt.Println("Failed to run load test: " + err.Error())
	}
}
