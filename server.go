package main

import (
	"github.com/buoyantio/slow_cooker/route"
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

	mutex sync.mutex
}

func NewServer(serverPort int) *Server {
	return &Server{
		Port:         serverPort,
		Service:      NewRestfulService(),
		Benchmarks:   make(map[string]*BenchmarkState),
		Calibrations: make(map[string]*CalibrationState),
	}
}

func (server *Server) Run() {
	restful.Add(server.Service)
	log.Printf("Server is start and running on port :%v", server.Port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%v", server.Port), nil))
}

// NewRestfulService : WebServer
func (server *Server) NewRestfulService() *restful.WebService {
	service := new(restful.WebService)
	service.Path("/slowcooker").Consumes(restful.MIME_JSON).Produces(restful.MIME_JSON)
	service.Route(service.POST("/benchmark").To(server.RunTest))
	service.Route(service.POST("/calibrate").To(server.RunCalibration))
	return service
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

	if _, ok := server.Benchmarks[singleLoad.LoadId]; ok {
		response.WriteError(http.StatusBadRequest, fmt.Errorf("Benchmark ID
	}

	go func() {
		// TODO: Track this run so we can return status of this run
		singleLoad.Run()
	}()

	response.WriteHeader(http.StatusAccepted)
}

func (server *Server) RunCalibration(request *restful.Request, response *restful.Response) {
	calibration := load.LatencyCalibration{}
	err := request.ReadEntity(&calibration)
	if err != nil {
		response.WriteError(http.StatusBadRequest, errors.New("Unable to deserialize calibration request: "+err.Error()))
		return
	}

	// TODO: Track this run so we can return status of this run
	go func() {
		calibration.Run()
	}()

	response.WriteHeader(http.StatusAccepted)
}
