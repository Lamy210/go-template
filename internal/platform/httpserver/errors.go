package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/Lamy210/go-template/internal/core/apperror"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5/middleware"
)

const internalErrorCode = "internal_error"

type errorDetail struct {
	Field   string `json:"field,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

type errorResponse struct {
	status int

	Code      string        `json:"code"`
	Message   string        `json:"message"`
	RequestID string        `json:"request_id,omitempty"`
	Details   []errorDetail `json:"details,omitempty"`
}

func (e *errorResponse) Error() string {
	return e.Message
}

func (e *errorResponse) GetStatus() int {
	return e.status
}

// toHTTPError maps transport-neutral application errors into a safe HTTP
// response. Unknown errors are intentionally collapsed to a generic 500.
func toHTTPError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}

	appErr, ok := errors.AsType[*apperror.Error](err)
	if !ok {
		return newErrorResponse(
			ctx,
			http.StatusInternalServerError,
			internalErrorCode,
			"internal server error",
			nil,
		)
	}

	status, mapped := statusForKind(appErr.Kind())
	if !mapped {
		return newErrorResponse(
			ctx,
			http.StatusInternalServerError,
			internalErrorCode,
			"internal server error",
			nil,
		)
	}

	code := string(appErr.Code())
	if code == "" {
		code = fallbackCodeForStatus(status)
	}

	message := appErr.PublicMessage()
	if message == "" {
		message = fallbackMessageForStatus(status)
	}

	details := appErr.Details()
	httpDetails := make([]errorDetail, 0, len(details))
	for _, detail := range details {
		httpDetails = append(httpDetails, errorDetail{
			Field:   detail.Field,
			Code:    string(detail.Code),
			Message: detail.Message,
		})
	}

	return newErrorResponse(ctx, status, code, message, httpDetails)
}

// transformHumaError normalizes Huma-generated validation and framework errors
// into the same public response contract without mutating Huma's global error
// factory. Raw 5xx details are intentionally discarded.
func transformHumaError(ctx huma.Context, statusText string, value any) (any, error) {
	model, ok := value.(*huma.ErrorModel)
	if !ok {
		return value, nil
	}

	status := model.Status
	if status == 0 {
		if parsed, err := strconv.Atoi(statusText); err == nil {
			status = parsed
		}
	}
	if status == 0 {
		status = http.StatusInternalServerError
	}

	message := model.Detail
	if status >= http.StatusInternalServerError || message == "" {
		message = fallbackMessageForStatus(status)
	}

	var details []errorDetail
	if status < http.StatusInternalServerError {
		details = make([]errorDetail, 0, len(model.Errors))
		for _, detail := range model.Errors {
			if detail == nil {
				continue
			}
			details = append(details, errorDetail{
				Field:   detail.Location,
				Message: detail.Message,
			})
		}
	}

	return newErrorResponse(
		ctx.Context(),
		status,
		fallbackCodeForStatus(status),
		message,
		details,
	), nil
}

func writeInternalErrorResponse(w http.ResponseWriter, r *http.Request) {
	response := newErrorResponse(
		r.Context(),
		http.StatusInternalServerError,
		internalErrorCode,
		"internal server error",
		nil,
	)

	body, err := json.Marshal(response)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write(append(body, '\n'))
}

func newErrorResponse(
	ctx context.Context,
	status int,
	code string,
	message string,
	details []errorDetail,
) *errorResponse {
	return &errorResponse{
		status:    status,
		Code:      code,
		Message:   message,
		RequestID: middleware.GetReqID(ctx),
		Details:   details,
	}
}

func statusForKind(kind apperror.Kind) (int, bool) {
	switch kind {
	case apperror.KindInvalidArgument:
		return http.StatusBadRequest, true
	case apperror.KindUnauthenticated:
		return http.StatusUnauthorized, true
	case apperror.KindPermissionDenied:
		return http.StatusForbidden, true
	case apperror.KindNotFound:
		return http.StatusNotFound, true
	case apperror.KindConflict:
		return http.StatusConflict, true
	case apperror.KindResourceExhausted:
		return http.StatusTooManyRequests, true
	case apperror.KindUnavailable:
		return http.StatusServiceUnavailable, true
	case apperror.KindInternal:
		return http.StatusInternalServerError, true
	default:
		return http.StatusInternalServerError, false
	}
}

func fallbackCodeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return "invalid_request"
	case http.StatusUnauthorized:
		return "unauthenticated"
	case http.StatusForbidden:
		return "permission_denied"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusUnsupportedMediaType:
		return "unsupported_media_type"
	case http.StatusTooManyRequests:
		return "resource_exhausted"
	case http.StatusServiceUnavailable:
		return "unavailable"
	default:
		if status >= http.StatusInternalServerError {
			return internalErrorCode
		}
		return "http_error"
	}
}

func fallbackMessageForStatus(status int) string {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return "invalid request"
	case http.StatusUnauthorized:
		return "authentication required"
	case http.StatusForbidden:
		return "permission denied"
	case http.StatusNotFound:
		return "resource not found"
	case http.StatusConflict:
		return "resource conflict"
	case http.StatusRequestEntityTooLarge:
		return "request body too large"
	case http.StatusUnsupportedMediaType:
		return "unsupported media type"
	case http.StatusTooManyRequests:
		return "resource limit exceeded"
	case http.StatusServiceUnavailable:
		return "service unavailable"
	default:
		if status >= http.StatusInternalServerError {
			return "internal server error"
		}
		return http.StatusText(status)
	}
}
