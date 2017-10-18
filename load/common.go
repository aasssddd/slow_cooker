package load

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const (
	// DayInMs 1 day in milliseconds
	DayInMs int64 = 24 * 60 * 60 * 1000000
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

func LoadData(data string) [][]byte {
	var file io.ReadCloser
	var requestData [][]byte
	var err error
	if strings.HasPrefix(data, "@") {
		path := data[1:]
		if path == "-" {
			file = os.Stdin
		} else if strings.HasPrefix(path, "http") {
			// Download file from remote host
			var resp *http.Response
			if resp, err = http.Get(path); err == nil {
				file = resp.Body
			} else {
				fmt.Fprintf(os.Stderr, err.Error())
				os.Exit(1)
			}
		} else {
			file, err = os.Open(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, err.Error())
				os.Exit(1)
			}
			defer file.Close()
		}
		reader := bufio.NewReader(file)
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			requestData = append(requestData, []byte(scanner.Text()))
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, err.Error())
			os.Exit(1)
		}
	} else {
		requestData = append(requestData, []byte(data))
	}

	return requestData
}
