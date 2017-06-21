package load

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"
)

const (
	// DayInMs 1 day in milliseconds
	DayInMs                 int64  = 24 * 60 * 60 * 1000000
	ServerBackendPrometheus string = "prometheus"
	ServerBackendInfluxDB   string = "influxdb"
)

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

func LoadData(data string) []byte {
	var file *os.File
	var requestData []byte
	var err error
	if strings.HasPrefix(data, "@") {
		path := data[1:]
		if path == "-" {
			file = os.Stdin
		} else {
			file, err = os.Open(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, err.Error())
				os.Exit(1)
			}
			defer file.Close()
		}

		requestData, err = ioutil.ReadAll(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, err.Error())
			os.Exit(1)
		}
	} else {
		requestData = []byte(data)
	}

	return requestData
}
