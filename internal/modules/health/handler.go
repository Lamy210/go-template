package health

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// ReadinessCheck returns nil when the process can accept traffic.
type ReadinessCheck func(context.Context) error

type response struct {
	Body struct {
		Status string `json:"status" example:"ok" doc:"Health status"`
	}
}

// Register installs liveness and readiness endpoints with stable operation IDs.
func Register(api huma.API, ready ReadinessCheck) {
	huma.Register(api, huma.Operation{
		OperationID: "health-live",
		Method:      http.MethodGet,
		Path:        "/health/live",
		Summary:     "Liveness probe",
		Tags:        []string{"Health"},
	}, func(context.Context, *struct{}) (*response, error) {
		out := &response{}
		out.Body.Status = "ok"
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "health-ready",
		Method:      http.MethodGet,
		Path:        "/health/ready",
		Summary:     "Readiness probe",
		Tags:        []string{"Health"},
	}, func(ctx context.Context, _ *struct{}) (*response, error) {
		if ready != nil {
			if err := ready(ctx); err != nil {
				return nil, huma.Error503ServiceUnavailable("service not ready", err)
			}
		}

		out := &response{}
		out.Body.Status = "ok"
		return out, nil
	})
}
