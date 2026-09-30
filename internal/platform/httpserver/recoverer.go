package httpserver

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Lamy210/go-template/internal/httpmethod"
	"github.com/go-chi/chi/v5/middleware"
)

// safeRecoverer contains application-handler panics without logging panic
// values or stack traces. A fresh response is normalized into the public error
// contract; a response that has already started is aborted instead of appending
// a misleading error body.
func safeRecoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tracked := w
			if _, ok := w.(middleware.WrapResponseWriter); !ok {
				tracked = middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			}

			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}
				if isAbortHandlerPanic(recovered) {
					panic(http.ErrAbortHandler)
				}

				r = ensureRecoveryRequestID(tracked, r)
				logger.ErrorContext(
					r.Context(),
					"http handler panic",
					"request_id", middleware.GetReqID(r.Context()),
					"method", httpmethod.LowCardinality(r.Method),
				)

				if responseStarted(tracked) ||
					strings.EqualFold(r.Header.Get("Connection"), "Upgrade") {
					panic(http.ErrAbortHandler)
				}

				writeInternalErrorResponse(tracked, r)
			}()

			next.ServeHTTP(tracked, r)
		})
	}
}

func isAbortHandlerPanic(value any) bool {
	err, ok := value.(error)
	return ok && errors.Is(err, http.ErrAbortHandler)
}

func responseStarted(w http.ResponseWriter) bool {
	wrapped, ok := w.(middleware.WrapResponseWriter)
	return ok && (wrapped.Status() != 0 || wrapped.BytesWritten() != 0)
}
