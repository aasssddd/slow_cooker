package route

import (
	"errors"
	"net/http"

	"github.com/buoyantio/slow_cooker/load"
	restful "github.com/emicklei/go-restful"
)

// RunTest : Run test
func RunTest(request *restful.Request, response *restful.Response) {
	singleLoad := load.AppLoad{}
	err := request.ReadEntity(&singleLoad)
	if err != nil {
		response.WriteError(http.StatusBadRequest, err)
		return
	}

	if singleLoad.TotalRequests == 0 {
		response.WriteError(http.StatusBadRequest, errors.New("TotalRequests cannot not be 0"))
		return
	}

	go func() {
		// TODO: Track this run so we can return status of this run
		singleLoad.Run()
	}()

	response.WriteHeader(http.StatusAccepted)
}

func RunCalibration(request *restful.Request, response *restful.Response) {
	calibration := load.LatencyCalibration{}
	err := request.ReadEntity(&calibration)
	if err != nil {
		response.WriteError(http.StatusBadRequest, errors.New("Unable to deserialize calibration request: "+err.Error()))
		return
	}

	// TODO: Track this run so we can return status of this run
	err = calibration.Run()
	if err != nil {
		response.WriteError(http.StatusBadRequest, errors.New("Unable to run calibration: "+err.Error()))
		return
	}

	response.WriteHeader(http.StatusAccepted)
}
