package httpserver

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// ServiceInfo is the HTTP-visible subset of process metadata.
type ServiceInfo struct {
	Service   string `json:"service" doc:"Service name"`
	Version   string `json:"version" doc:"Application version"`
	Commit    string `json:"commit" doc:"Source commit identifier"`
	BuildTime string `json:"build_time" doc:"Build timestamp or reproducible-build marker"`
}

type versionResponse struct {
	Body ServiceInfo
}

func registerVersion(api huma.API, info ServiceInfo) {
	registerOperation(api, huma.Operation{
		OperationID: "service-version",
		Method:      http.MethodGet,
		Path:        "/version",
		Summary:     "Service build information",
		Tags:        []string{"Service"},
	}, func(context.Context, *struct{}) (*versionResponse, error) {
		return &versionResponse{Body: info}, nil
	})
}
