package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/buoyantio/slow_cooker/load"
	restful "github.com/emicklei/go-restful"
)

const (
	modeLoad       string = "load"
	modeServer     string = "server"
	modeLatency    string = "latency"
	modeThroughput string = "throughput"
)

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
	qosLatency := flag.Duration("qos-latency", 0, "Set latency goal")
	qosThroughput := flag.Int("qos-throughput", 0, "Set throughput goal")
	qosConfidenceTimes := flag.Int("qos-confidence-times", 2, "Set how many times to do verify regressive")
	qosTolerencePercentage := flag.Float64("qos-tolerence-percentage", 0.1, "Set 0.1 means 10% offset is allow")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s <url> [flags]\n", path.Base(os.Args[0]))
		flag.PrintDefaults()
	}

	flag.Parse()
	var params load.Load

	if *mode == modeServer {
		restful.Add(NewRestfulService())
		log.Printf("server is start and running on localhost:%v", *serverPort)
		log.Fatal(http.ListenAndServe(fmt.Sprintf(":%v", *serverPort), nil))
	} else {
		if *help {
			flag.Usage()
			os.Exit(64)
		}

		if flag.NArg() != 1 {
			load.ExUsage("Expecting one argument: the target url to test, e.g. http://localhost:4140/")
		}

		urldest := flag.Arg(0)
		dstURL, err := url.Parse(urldest)
		if err != nil {
			load.ExUsage("invalid URL: '%s': %s\n", urldest, err.Error())
		}

		if *qps < 1 {
			load.ExUsage("qps must be at least 1")
		}

		if *concurrency < 1 {
			load.ExUsage("concurrency must be at least 1")
		}

		if *qosTolerencePercentage > 1 {
			load.ExUsage("tolerence percentage must between 0 and 1")
		}

		hosts := strings.Split(*host, ",")

		requestData := load.LoadData(*data)

		switch *mode {
		case modeLoad:
			params = &load.RunLoadParams{
				Qps:                  *qps,
				Concurrency:          *concurrency,
				Method:               *method,
				Interval:             *interval,
				Noreuse:              *noreuse,
				Compress:             *compress,
				NoLatencySummary:     *noLatencySummary,
				ReportLatenciesCSV:   *reportLatenciesCSV,
				TotalRequests:        *totalRequests,
				Headers:              headers,
				MetricAddr:           *metricAddr,
				HashValue:            *hashValue,
				HashSampleRate:       *hashSampleRate,
				DstURL:               *dstURL,
				Hosts:                hosts,
				RequestData:          requestData,
				MetricsServerBackend: *metricsServerBackend,
				InfluxUsername:       *influxUsername,
				InfluxPassword:       *influxPassword,
				InfluxDatabase:       *influxDatabase,
				HistogramWindowSize:  *histogramWindowSize,
			}
		case modeLatency:
			params = &load.RunCalibrationParams{
				Qos: load.Qos{
					Latency:             *qosLatency,
					ConfidenceTimes:     *qosConfidenceTimes,
					TolerencePrecentage: *qosTolerencePercentage},
				Concurrency:    *concurrency,
				Method:         *method,
				Interval:       *interval,
				Noreuse:        *noreuse,
				Compress:       *compress,
				Headers:        headers,
				HashValue:      *hashValue,
				HashSampleRate: *hashSampleRate,
				DstURL:         *dstURL,
				Hosts:          hosts,
				RequestData:    requestData,
			}
		case modeThroughput:
			params = &load.RunCalibrationParams{
				Qos:            load.Qos{Throughput: *qosThroughput},
				Concurrency:    *concurrency,
				Method:         *method,
				Interval:       *interval,
				Noreuse:        *noreuse,
				Compress:       *compress,
				Headers:        headers,
				HashValue:      *hashValue,
				HashSampleRate: *hashSampleRate,
				DstURL:         *dstURL,
				Hosts:          hosts,
				RequestData:    requestData,
			}
		default:
			load.ExUsage("-mode must in one of load/server/latency/throughput")
		}

		load.Run(params)
	}

}
