package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/buoyantio/slow_cooker/load"
	restful "github.com/emicklei/go-restful"
)

const (
	RUNNING_STATE  = "running"
	FINISHED_STATE = "finished"
	FAILED_STATE   = "failed"
)

type BenchmarkState struct {
	Id      string
	Error   string
	State   string
	Results []*load.BenchmarkRecord
}

type CalibrationState struct {
	Id       string
	Results  []*load.CalibrationRecord
	FinalQps int
	Error    string
	State    string
}

type Server struct {
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
	newRestfulService(server)
	return server
}

func (server *Server) Run() {
	log.Printf("Server is start and running on port :%v", server.Port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%v", server.Port), nil))
}

// NewRestfulService : WebServer
func newRestfulService(server *Server) {
	service := new(restful.WebService)
	service.Path("/slowcooker").Consumes(restful.MIME_JSON).Produces(restful.MIME_JSON)
	service.Route(service.POST("/benchmark").To(server.RunBenchmark))
	service.Route(service.GET("/benchmark/{RunId}").To(server.GetBenchmarkRunningState))
	service.Route(service.POST("/calibrate").To(server.RunCalibration))
	service.Route(service.GET("/calibrate/{RunId}").To(server.GetCalibrateRunningState))
	restful.Add(service)
}

// RunTest : Run test
func (server *Server) RunBenchmark(request *restful.Request, response *restful.Response) {
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

	id := singleLoad.RunId
	if id == "" {
		response.WriteError(http.StatusBadRequest, fmt.Errorf("Benchmark ID not provided"))
		return
	}

	server.mutex.Lock()
	defer server.mutex.Unlock()

	if _, ok := server.Benchmarks[id]; !ok {
		response.WriteError(http.StatusBadRequest, fmt.Errorf("Task exist"))
		return
	}

	state := &BenchmarkState{Id: id, State: RUNNING_STATE}
	server.Benchmarks[id] = state

	loadDuration, err := time.ParseDuration(singleLoad.LoadTime)
	if err != nil {
		response.WriteError(http.StatusBadRequest, fmt.Errorf("Unable to parse load time: "+err.Error()))
		return
	}

	go func() {
		for i := 0; i < 3; i++ {
			go func() {
				singleLoad.Run()
			}()

			<-time.After(loadDuration)
			singleLoad.Stop()
			newRecord := &load.BenchmarkRecord{
				PercentileMin: singleLoad.HandlerParams.GlobalHist.Min(),
				Percentile50:  singleLoad.HandlerParams.GlobalHist.ValueAtQuantile(50),
				Percentile95:  singleLoad.HandlerParams.GlobalHist.ValueAtQuantile(95),
				Percentile99:  singleLoad.HandlerParams.GlobalHist.ValueAtQuantile(99),
				PercentileMax: singleLoad.HandlerParams.GlobalHist.Max(),
			}

			state.Results = append(state.Results, newRecord)
		}

		state.State = FINISHED_STATE
	}()

	response.WriteHeader(http.StatusAccepted)
}

func (server *Server) GetBenchmarkRunningState(request *restful.Request, response *restful.Response) {
	id := request.PathParameter("RunId")
	if id == "" {
		response.WriteError(http.StatusBadRequest, fmt.Errorf("Benchmark ID not provided"))
		return
	}

	if value, ok := server.Benchmarks[id]; !ok {
		response.WriteError(http.StatusBadRequest, fmt.Errorf("Benchmark ID not exists"))
	} else {
		response.WriteEntity(value)
	}
}

func (server *Server) RunCalibration(request *restful.Request, response *restful.Response) {
	calibration := load.LatencyCalibration{}
	if err := request.ReadEntity(&calibration); err != nil {
		response.WriteError(http.StatusBadRequest, errors.New("Unable to deserialize calibration request: "+err.Error()))
		return
	}

	id := calibration.Load.RunId
	if id == "" {
		response.WriteError(http.StatusBadRequest, fmt.Errorf("Calibrate ID not provided"))
		return
	}

	server.mutex.Lock()
	defer server.mutex.Unlock()

	if _, exist := server.Calibrations[id]; exist {
		response.WriteError(http.StatusBadRequest, fmt.Errorf("Task exist"))
		return
	}

	state := &CalibrationState{Id: id, State: RUNNING_STATE}
	server.Calibrations[calibration.Load.RunId] = state

	go func() {
		finalQps, err := calibration.Run()
		if err != nil {
			state.Error = err.Error()
			state.State = FAILED_STATE
		} else {
			state.Results = calibration.Results
			state.FinalQps = finalQps
			state.State = FINISHED_STATE
		}
	}()

	response.WriteHeader(http.StatusAccepted)
}

func (server *Server) GetCalibrateRunningState(request *restful.Request, response *restful.Response) {
	id := request.PathParameter("RunId")
	if id == "" {
		response.WriteError(http.StatusBadRequest, fmt.Errorf("Calibrate ID not provided"))
		return
	}

	if value, ok := server.Calibrations[id]; ok {
		response.WriteEntity(value)
	} else {
		response.WriteError(http.StatusBadRequest, fmt.Errorf("Calibrate ID not exists"))
	}
}
