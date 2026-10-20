package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// appCSP is the policy for the app shell, prerendered pages and JSON. Fonts
// come from Google Fonts (the only third party the frontend loads); images
// may come from any https origin because reports embed repository avatars.
const appCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
	"font-src https://fonts.gstatic.com 'self' data:; img-src 'self' data: https:; connect-src 'self' https: wss:; " +
	"frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

// docsCSP relaxes the policy for the Scalar reference, which loads its bundle
// from jsdelivr and injects inline styles.
const docsCSP = "default-src 'self'; script-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; " +
	"style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net https://fonts.googleapis.com; " +
	"font-src 'self' data: https://fonts.gstatic.com https://cdn.jsdelivr.net; img-src 'self' data: https:; " +
	"connect-src 'self' https://cdn.jsdelivr.net; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

// securityHeaders sets the response headers every reply carries. Handlers
// that need a different CSP (Scalar) overwrite it before writing.
func securityHeaders(prod bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			h.Set("Content-Security-Policy", appCSP)
			if prod {
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			if id := middleware.GetReqID(r.Context()); id != "" {
				h.Set("X-Request-Id", id)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requestLogger replaces chi's text logger with one slog line per request.
// The route pattern (not the raw path) is logged so ids never reach the log.
func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			route := routePattern(r)
			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}
			dur := time.Since(start)
			lvl := slog.LevelInfo
			if status >= 500 {
				lvl = slog.LevelError
			}
			log.LogAttrs(r.Context(), lvl, "request",
				slog.String("method", r.Method),
				slog.String("route", route),
				slog.Int("status", status),
				slog.Int("bytes", ww.BytesWritten()),
				slog.Float64("duration_ms", float64(dur.Microseconds())/1000),
				slog.String("request_id", middleware.GetReqID(r.Context())),
				slog.String("ip", clientIP(r.RemoteAddr)),
				slog.String("user_agent", r.UserAgent()),
			)
			observe(r.Method, route, status, dur)
		})
	}
}

// routePattern is the chi pattern that served the request, or the path
// class for requests nothing matched (static files, SPA fallback).
func routePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if p := rc.RoutePattern(); p != "" {
			return p
		}
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/assets/"):
		return "/assets/*"
	case strings.HasPrefix(r.URL.Path, "/v1/"):
		return "/v1/*"
	}
	return "/*"
}

// ---------- metrics ----------

var (
	metricsOnce     sync.Once
	metricsRegistry *prometheus.Registry
	reqTotal        *prometheus.CounterVec
	reqDuration     *prometheus.HistogramVec
	rateLimitedTot  *prometheus.CounterVec
	inFlight        prometheus.Gauge
)

func initMetrics() {
	metricsOnce.Do(func() {
		metricsRegistry = prometheus.NewRegistry()
		reqTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vig_http_requests_total", Help: "Requests served, by route pattern, method and status.",
		}, []string{"route", "method", "status"})
		reqDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "vig_http_request_duration_seconds", Help: "Request latency, by route pattern, method and status.",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"route", "method", "status"})
		rateLimitedTot = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vig_rate_limited_total", Help: "Requests refused with 429, by limiter scope.",
		}, []string{"scope"})
		inFlight = prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "vig_http_in_flight_requests", Help: "Requests currently being served.",
		})
		metricsRegistry.MustRegister(reqTotal, reqDuration, rateLimitedTot, inFlight,
			collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
			collectors.NewBuildInfoCollector())
	})
}

func observe(method, route string, status int, dur time.Duration) {
	initMetrics()
	s := strconv.Itoa(status)
	reqTotal.WithLabelValues(route, method, s).Inc()
	reqDuration.WithLabelValues(route, method, s).Observe(dur.Seconds())
}

func countRateLimited(scope string) {
	initMetrics()
	rateLimitedTot.WithLabelValues(scope).Inc()
}

func inFlightMiddleware(next http.Handler) http.Handler {
	initMetrics()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inFlight.Inc()
		defer inFlight.Dec()
		next.ServeHTTP(w, r)
	})
}

// metricsHandler exposes the process registry in the Prometheus text format.
func metricsHandler() http.Handler {
	initMetrics()
	return promhttp.HandlerFor(metricsRegistry, promhttp.HandlerOpts{})
}

// ---------- readiness ----------

// readyz pings the database. The liveness probe (/healthz) is a huma
// operation; readiness sits outside the spec because its failure body is not
// something a client should code against.
func (d *Deps) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	w.Header().Set("Cache-Control", "no-store")
	if d.Store == nil || d.Store.Pool == nil {
		writeProblem(w, r, http.StatusServiceUnavailable, "database not configured")
		return
	}
	if err := d.Store.Pool.Ping(ctx); err != nil {
		writeProblem(w, r, http.StatusServiceUnavailable, "database unreachable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}` + "\n"))
}

// writeProblem writes an RFC 9457 body for handlers that live outside huma.
func writeProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	body := map[string]any{"status": status, "title": http.StatusText(status), "detail": detail}
	if id := middleware.GetReqID(r.Context()); id != "" {
		body["instance"] = "urn:request:" + id
	}
	_ = writeJSON(w, body)
}

func writeJSON(w http.ResponseWriter, v any) error {
	return json.NewEncoder(w).Encode(v)
}

// contextLogger returns the logger carrying the request id when one exists.
func contextLogger(ctx context.Context) *slog.Logger {
	if id := middleware.GetReqID(ctx); id != "" {
		return slog.Default().With("request_id", id)
	}
	return slog.Default()
}

// withRequestID copies the request id into huma responses. It runs first in
// the huma chain so error bodies written by later middleware carry it too.
func withRequestID(ctx huma.Context, next func(huma.Context)) {
	if id := middleware.GetReqID(ctx.Context()); id != "" {
		ctx.SetHeader("X-Request-Id", id)
	}
	next(ctx)
}
