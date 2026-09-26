package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/Lamy210/go-template/internal/core/apperror"
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

	status := statusForKind(appErr.Kind())
	code := string(appErr.Code())
	if code == "" {
		code = fallbackCodeForKind(appErr.Kind())
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

func statusForKind(kind apperror.Kind) int {
	switch kind {
	case apperror.KindInvalidArgument:
		return http.StatusBadRequest
	case apperror.KindUnauthenticated:
		return http.StatusUnauthorized
	case apperror.KindPermissionDenied:
		return http.StatusForbidden
	case apperror.KindNotFound:
		return http.StatusNotFound
	case apperror.KindConflict:
		return http.StatusConflict
	case apperror.KindResourceExhausted:
		return http.StatusTooManyRequests
	case apperror.KindUnavailable:
		return http.StatusServiceUnavailable
	case apperror.KindInternal:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

func fallbackCodeForKind(kind apperror.Kind) string {
	switch kind {
	case apperror.KindInvalidArgument:
		return "invalid_argument"
	case apperror.KindUnauthenticated:
		return "unauthenticated"
	case apperror.KindPermissionDenied:
		return "permission_denied"
	case apperror.KindNotFound:
		return "not_found"
	case apperror.KindConflict:
		return "conflict"
	case apperror.KindResourceExhausted:
		return "resource_exhausted"
	case apperror.KindUnavailable:
		return "unavailable"
	default:
		return internalErrorCode
	}
}

func fallbackMessageForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid request"
	case http.StatusUnauthorized:
		return "authentication required"
	case http.StatusForbidden:
		return "permission denied"
	case http.StatusNotFound:
		return "resource not found"
	case http.StatusConflict:
		return "resource conflict"
	case http.StatusTooManyRequests:
		return "resource limit exceeded"
	case http.StatusServiceUnavailable:
		return "service unavailable"
	default:
		return "internal server error"
	}
}
