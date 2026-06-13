package httpapi

import (
	"bufio"
	"errors"
	"net"
	"net/http"

	"github.com/huangchengsir/pipewright/internal/i18n"
)

// localeResponseWriter carries the resolved request locale so writeError can
// localize messages without threading a parameter through ~250 call sites.
//
// It forwards Flush/Hijack/Unwrap to the underlying writer so the streaming
// paths keep working through the wrapper:
//   - SSE (run logs, server logs, version stream) type-assert w.(http.Flusher).
//   - The WS terminal (coder/websocket) reaches the conn via http.Hijacker /
//     http.ResponseController, which traverse Unwrap.
type localeResponseWriter struct {
	http.ResponseWriter
	locale string
}

func (w *localeResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *localeResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("underlying ResponseWriter does not support Hijack")
}

// Unwrap exposes the wrapped writer to http.ResponseController (Go 1.20+),
// which coder/websocket and stdlib helpers use to find optional interfaces.
func (w *localeResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// localeMiddleware resolves the request locale (UI-locale header first, then
// Accept-Language) and wraps the writer so writeError can localize responses.
func localeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loc := i18n.FromHeaders(r.Header.Get("X-Pipewright-Locale"), r.Header.Get("Accept-Language"))
		next.ServeHTTP(&localeResponseWriter{ResponseWriter: w, locale: loc}, r)
	})
}

// localeOf extracts the resolved locale from a (possibly wrapped) writer.
func localeOf(w http.ResponseWriter) string {
	if lw, ok := w.(*localeResponseWriter); ok {
		return lw.locale
	}
	return i18n.Default
}
