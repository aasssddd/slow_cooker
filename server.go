package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/buoyantio/slow_cooker/load"
	restful "github.com/emicklei/go-restful"
)

type BenchmarkState struct {
	Id string
}

type CalibrationState struct {
	Id string
}

type Server struct {
	Service      *restful.WebService
	Benchmarks   map[string]*BenchmarkState
	Calibrations map[string]*CalibrationState
	Port         int
	mutex        sync.Mutex
}

func NewServer(serverPort int) *Server {
	server := &Server{
		Port:         serverPort,
		Benchmarks:   make(map[string]*BenchmarkState),
		Calibrations: make(map[string]*CalibrationState),
	}
	NewRestfulService(server)
	return server
}

func (server *Server) Run() {
	restful.Add(server.Service)
	log.Printf("Server is start and running on port :%v", server.Port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%v", server.Port), nil))
}

// NewRestfulService : WebServer
func NewRestfulService(server *Server) {
	service := new(restful.WebService)
	service.Path("/slowcooker").Consumes(restful.MIME_JSON).Produces(restful.MIME_JSON)
	service.Route(service.POST("/benchmark").To(server.RunTest))
	service.Route(service.GET("/benchmark/{RunId}").To(server.GetBenchmarkRunningState))
	service.Route(service.POST("/calibrate").To(server.RunCalibration))
	service.Route(service.GET("/calibrate/{RunId}").To(server.GetCalibrateRunningState))
	server.Service = service
}

// RunTest : Run test
func (server *Server) RunTest(request *restful.Request, response *restful.Response) {
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

	server.mutex.Lock()
	defer server.mutex.Unlock()

	if id := singleLoad.RunId; id == "" {
		response.WriteError(http.StatusBadRequest, fmt.Errorf("Benchmark ID not provided"))
	} else if _, ok := server.Benchmarks[id]; !ok {
		response.WriteError(http.StatusBadRequest, fmt.Errorf("Task exist"))
	} else {
		server.Benchmarks[id] = &BenchmarkState{Id: id}
		go func() {
			singleLoad.Run()
		}()
		// Track this run so we can return status of this run
		go func() {
			for {
				if <-singleLoad.ListenEvent() {
					// complete
					delete(server.Benchmarks, id)
					break
				}
			}
		}()
	}

	response.WriteHeader(http.StatusAccepted)
}

func (server *Server) GetBenchmarkRunningState(request *restful.Request, response *restful.Response) {
	if id := request.PathParameter("RunId"); id == "" {
		response.WriteError(http.StatusBadRequest, fmt.Errorf("Benchmark ID not provided"))
	} else if value, ok := server.Benchmarks[id]; ok {
		response.WriteEntity(value)
	} else {
		response.WriteError(http.StatusBadRequest, fmt.Errorf("Benchmark ID not exists"))
	}
}

func (server *Server) RunCalibration(request *restful.Request, response *restful.Response) {
	calibration := load.LatencyCalibration{}
	err := request.ReadEntity(&calibration)
	if err != nil {
		response.WriteError(http.StatusBadRequest, errors.New("Unable to deserialize calibration request: "+err.Error()))
		return
	}

	// TODO: Track this run so we can return status of this run
	if id := calibration.Load.RunId; id == "" {
		response.WriteError(http.StatusBadRequest, fmt.Errorf("Calibrate ID not provided"))
	} else if _, exist := server.Calibrations[id]; exist {
		response.WriteError(http.StatusBadRequest, fmt.Errorf("Task exist"))
	} else {
		go func() {
			server.Calibrations[calibration.Load.RunId] = &CalibrationState{Id: id}
			calibration.Run()
		}()
		go func() {
			for {
				if <-calibration.Load.ListenEvent() {
					// complete
					delete(server.Calibrations, id)
					break
				}
			}
		}()
	}

	response.WriteHeader(http.StatusAccepted)
}

func (server *Server) GetCalibrateRunningState(request *restful.Request, response *restful.Response) {
	if id := request.PathParameter("RunId"); id == "" {
		response.WriteError(http.StatusBadRequest, fmt.Errorf("Calibrate ID not provided"))
	} else if value, ok := server.Calibrations[id]; ok {
		response.WriteEntity(value)
	} else {
		response.WriteError(http.StatusBadRequest, fmt.Errorf("Calibrate ID not exists"))
	}
}
