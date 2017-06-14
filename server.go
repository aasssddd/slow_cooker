package main

import (
	"github.com/buoyantio/slow_cooker/route"
	restful "github.com/emicklei/go-restful"
)

// NewRestfulService : WebServer
func NewRestfulService() *restful.WebService {
	service := new(restful.WebService)
	service.Path("/slowcooker").Consumes(restful.MIME_JSON).Produces(restful.MIME_JSON)
	service.Route(service.POST("").To(route.RunTest))
	return service
}
