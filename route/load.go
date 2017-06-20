package route

import (
	"net/http"

	"github.com/buoyantio/slow_cooker/load"
	restful "github.com/emicklei/go-restful"
)

type Response struct {
	error bool
	data  string
}

// RunTest : Run test
func RunTest(request *restful.Request, response *restful.Response) {
	singleLoad := load.SingleLoad{}
	err := request.ReadEntity(&singleLoad)
	if err != nil {
		response.WriteError(http.StatusBadRequest, err)
		return
	}

	resp := &Response{}
	if singleLoad.TotalRequests == 0 {
		resp.error = true
		resp.data = "TotalRequests cannot not be 0"
		response.WriteError(http.StatusBadRequest, resp)
		return
	}

	go func() {
		// TODO: Track this run so we can return status of this run
		singleLoad.Run()
	}()

	resp.status = statusOk
	resp.message = "Load test is running now"
	if err := response.WriteEntity(resp); err != nil {
		response.WriteError(http.StatusInternalServerError, err)
	}
}
