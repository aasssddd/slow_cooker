package route

import (
	"net/http"

	"github.com/buoyantio/slow_cooker/load"
	restful "github.com/emicklei/go-restful"
)

const (
	statusWrong string = "Wrong"
	statusOk    string = "Ok"
)

// RunTest : Run test
func RunTest(request *restful.Request, response *restful.Response) {
	param := load.RunLoadParams{}
	err := request.ReadEntity(&param)
	if err != nil {
		response.WriteError(http.StatusBadRequest, err)
	}
	resp := &Response{}
	if param.TotalRequests == 0 {
		resp.status = statusWrong
		resp.message = "TotalRequests cannot not be 0"
		response.WriteEntity(resp)
		return
	}
	var ins load.Load = &param
	load.Run(ins)
	resp.status = statusOk
	resp.message = "Load test is running now"
	if err := response.WriteEntity(resp); err != nil {
		response.WriteErrorString(http.StatusInternalServerError, err.Error())
	}
}
