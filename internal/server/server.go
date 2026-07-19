package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const defaultMessage = "Hello from the multi-cloud demo application"

type Config struct {
	Environment string
	Cloud       string
	Message     string
	Port        string
	Version     string
}

type application struct {
	config       Config
	logger       *slog.Logger
	startedAt    time.Time
	requestCount atomic.Uint64
}

func ConfigFromEnvironment(version string) Config {
	return Config{
		Environment: environmentValue("APP_ENVIRONMENT", "local"),
		Cloud:       environmentValue("APP_CLOUD", "local"),
		Message:     environmentValue("RESPONSE_MESSAGE", defaultMessage),
		Port:        environmentValue("PORT", "8080"),
		Version:     version,
	}
}

func NewHandler(config Config, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
	}

	app := &application{
		config:    config,
		logger:    logger,
		startedAt: time.Now(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", app.root)
	mux.HandleFunc("GET /healthz", app.health)
	mux.HandleFunc("GET /readyz", app.ready)
	mux.HandleFunc("GET /metrics", app.metrics)

	return app.securityHeaders(app.requestLogging(mux))
}

func (app *application) root(writer http.ResponseWriter, request *http.Request) {
	app.writeJSON(writer, http.StatusOK, map[string]string{
		"service":     "multicloud-demo-app",
		"environment": app.config.Environment,
		"cloud":       app.config.Cloud,
		"message":     app.config.Message,
		"version":     app.config.Version,
	})
}

func (app *application) health(writer http.ResponseWriter, request *http.Request) {
	app.writeJSON(writer, http.StatusOK, map[string]string{"status": "healthy"})
}

func (app *application) ready(writer http.ResponseWriter, request *http.Request) {
	app.writeJSON(writer, http.StatusOK, map[string]string{"status": "ready"})
}

func (app *application) metrics(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	uptime := time.Since(app.startedAt).Seconds()
	_, _ = fmt.Fprintf(writer,
		"# HELP multicloud_demo_requests_total Total HTTP requests received.\n"+
			"# TYPE multicloud_demo_requests_total counter\n"+
			"multicloud_demo_requests_total %d\n"+
			"# HELP multicloud_demo_uptime_seconds Process uptime in seconds.\n"+
			"# TYPE multicloud_demo_uptime_seconds gauge\n"+
			"multicloud_demo_uptime_seconds %s\n",
		app.requestCount.Load(), strconv.FormatFloat(uptime, 'f', 3, 64),
	)
}

func (app *application) writeJSON(writer http.ResponseWriter, status int, payload any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(payload); err != nil {
		app.logger.Error("response encoding failed", "error", err)
	}
}

func (app *application) requestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		app.requestCount.Add(1)
		started := time.Now()
		next.ServeHTTP(writer, request)
		app.logger.Info("request completed",
			"method", request.Method,
			"path", request.URL.Path,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
}

func (app *application) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'")
		next.ServeHTTP(writer, request)
	})
}

func environmentValue(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
