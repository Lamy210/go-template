package httpserver

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
)

// ServiceInfo is the HTTP-visible subset of process metadata.
type ServiceInfo struct {
	Service   string `json:"service" doc:"Service name"`
	Version   string `json:"version" doc:"Application version"`
	Commit    string `json:"commit" doc:"Source commit identifier"`
	BuildTime string `json:"build_time" doc:"Build timestamp or reproducible-build marker"`
}

// Validate keeps HTTP-visible process/build identity on one stable JSON text
// contract before the version route is registered.
func (i ServiceInfo) Validate() error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "service", value: i.Service},
		{name: "version", value: i.Version},
		{name: "commit", value: i.Commit},
		{name: "build time", value: i.BuildTime},
	} {
		if strings.TrimSpace(field.value) == "" {
			return errors.New("http service metadata " + field.name + " must not be empty")
		}
		if !utf8.ValidString(field.value) {
			return errors.New("http service metadata " + field.name + " must be valid UTF-8")
		}
	}
	return nil
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
