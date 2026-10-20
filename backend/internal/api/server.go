package api

import (
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Version is set at build time.
var Version = "dev"

func humaConfig() huma.Config {
	cfg := huma.DefaultConfig("vig API", Version)
	cfg.Info.Description = "Solana program audits sold as credits. Every operation is typed. Operations that depend on a deferred integration (GitHub App, Discord, USDC treasury) answer 503 until it is configured."
	cfg.DocsPath = ""    // Scalar is served at /docs
	cfg.OpenAPIPath = "" // served by openAPIJSON, trimmed to the caller's level
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		secSession:  {Type: "apiKey", In: "cookie", Name: "vig_access", Description: "Short-lived access token cookie; refresh with POST /v1/auth/refresh"},
		secAPIKey:   {Type: "http", Scheme: "bearer", BearerFormat: "vig_<key>"},
		secOperator: {Type: "http", Scheme: "bearer", BearerFormat: "job-scoped operator token"},
		secAdmin:    {Type: "apiKey", In: "cookie", Name: "vig_access", Description: "Admin role and a TOTP check within 12 hours are required"},
	}
	return cfg
}

// NewRouter builds the chi router with huma, the OpenAPI document, Scalar,
// the operational endpoints and, when Config.StaticDir is set, the built
// frontend.
func NewRouter(d *Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, securityHeaders(d.Config.IsProd()), inFlightMiddleware, requestLogger(slog.Default()), middleware.Recoverer)

	api := humachi.New(r, humaConfig())
	d.api = api
	if !rateLimitDisabled() {
		d.limiter = newLimiter(d.now)
	}
	api.UseMiddleware(withRequestID, d.authMiddleware, d.rateLimit)
	Register(api, d)

	r.Get("/docs", scalar)
	r.Get("/openapi.json", d.openAPIJSON)
	r.Get("/llms.txt", d.llms)
	r.Get("/readyz", d.readyz)
	r.Get("/metrics", metricsHandler().ServeHTTP)
	d.mountOG(r)
	d.MountVisuals(r)

	if d.Config.StaticDir != "" {
		s, err := newStatic(d)
		if err != nil {
			slog.Error("static assets unavailable, serving the API only", "dir", d.Config.StaticDir, "err", err)
		} else {
			s.mount(r)
		}
	} else {
		r.NotFound(func(w http.ResponseWriter, r *http.Request) { writeProblem(w, r, http.StatusNotFound, "no such route") })
	}
	return r
}

// scalar serves the API reference. The reference script is the only external
// resource; it reads /openapi.json from this origin.
func scalar(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", docsCSP)
	w.Header().Set("X-Robots-Tag", "noindex")
	_, _ = w.Write([]byte(scalarHTML))
}

// scalarHTML maps Scalar's theme variables onto the app's dark tokens
// (frontend/src/styles/tokens.css) so the reference reads as one product.
const scalarHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>vig API</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Instrument+Sans:wght@400;500;600&family=Geist+Mono:wght@400;500&display=swap">
<style>
:root, .dark-mode, .light-mode {
  --scalar-background-1: #0f0f12; --scalar-background-2: #18181b; --scalar-background-3: #212225;
  --scalar-background-accent: #0e2e12;
  --scalar-color-1: #fafafa; --scalar-color-2: #a1a1aa; --scalar-color-3: #71717a;
  --scalar-color-accent: #86efac; --scalar-border-color: #27272a;
  --scalar-color-green: #82e28d; --scalar-color-red: #f87171; --scalar-color-yellow: #facc15;
  --scalar-color-blue: #86efac; --scalar-color-orange: #facc15; --scalar-color-purple: #c084fc;
  --scalar-sidebar-background-1: #0f0f12; --scalar-sidebar-color-1: #fafafa; --scalar-sidebar-color-2: #a1a1aa;
  --scalar-sidebar-color-active: #fafafa; --scalar-sidebar-border-color: #18181b;
  --scalar-sidebar-item-hover-background: #18181b; --scalar-sidebar-item-hover-color: #fafafa;
  --scalar-sidebar-item-active-background: #27272a;
  --scalar-sidebar-search-background: #18181b; --scalar-sidebar-search-border-color: #27272a; --scalar-sidebar-search-color: #a1a1aa;
  --scalar-button-1: #86efac; --scalar-button-1-color: #052e16; --scalar-button-1-hover: #a7f3c4;
  --scalar-font: "Instrument Sans", ui-sans-serif, system-ui, sans-serif;
  --scalar-font-code: "Geist Mono", ui-monospace, "SF Mono", Menlo, monospace;
  --scalar-radius: 6px; --scalar-radius-lg: 8px; --scalar-radius-xl: 8px;
}
body { background: #0f0f12; margin: 0; }
</style></head>
<body>
<script id="api-reference" data-url="/openapi.json" data-configuration='{"theme":"none","darkMode":true,"forceDarkModeState":"dark","hideDarkModeToggle":true,"hideModels":false}'></script>
<script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
</body></html>`
