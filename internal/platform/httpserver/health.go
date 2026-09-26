package httpserver

import (
	"context"
	"net/http"

	"github.com/Lamy210/go-template/internal/core/apperror"
	"github.com/danielgtaylor/huma/v2"
)

// ReadinessCheck returns nil when the process can accept traffic.
type ReadinessCheck func(context.Context) error

type healthResponse struct {
	Body struct {
		Status string `json:"status" example:"ok" doc:"Health status"`
	}
}

func registerHealth(api huma.API, ready ReadinessCheck) {
	registerOperation(api, huma.Operation{
		OperationID: "health-live",
		Method:      http.MethodGet,
		Path:        "/health/live",
		Summary:     "Liveness probe",
		Tags:        []string{"Health"},
	}, func(context.Context, *struct{}) (*healthResponse, error) {
		out := &healthResponse{}
		out.Body.Status = "ok"
		return out, nil
	})

	registerOperation(api, huma.Operation{
		OperationID: "health-ready",
		Method:      http.MethodGet,
		Path:        "/health/ready",
		Summary:     "Readiness probe",
		Tags:        []string{"Health"},
	}, func(ctx context.Context, _ *struct{}) (*healthResponse, error) {
		if ready != nil {
			if err := ready(ctx); err != nil {
				return nil, apperror.Wrap(
					err,
					apperror.KindUnavailable,
					"service_not_ready",
					"service not ready",
				)
			}
		}

		out := &healthResponse{}
		out.Body.Status = "ok"
		return out, nil
	})
}
