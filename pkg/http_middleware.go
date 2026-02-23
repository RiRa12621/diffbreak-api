package pkg

import (
	"net/http"
	"time"

	"go.uber.org/zap"
)

func (rec *statusRecorder) WriteHeader(code int) {
	rec.status = code
	rec.ResponseWriter.WriteHeader(code)
}

// WrapHandler instruments handlers with metrics and request logging.
func WrapHandler(handlerName string, handler http.Handler, logger *zap.Logger) http.Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}

		handler.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		duration := time.Since(start)
		observeHTTPRequest(handlerName, r.Method, status, duration)

		logger.Info("request completed",
			zap.String("handler", handlerName),
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.Int("status", status),
			zap.Duration("duration", duration),
		)
	})
}
