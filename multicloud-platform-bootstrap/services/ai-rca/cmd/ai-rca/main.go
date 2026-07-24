package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const maxWebhookBody = 1 << 20

type config struct {
	ListenAddress   string
	OllamaURL       string
	OllamaModel     string
	PrometheusURL   string
	SlackWebhookURL string
	WebhookToken    string
	GrafanaURL      string
	PrometheusUIURL string
	AlertmanagerURL string
	ArgoCDURL       string
	OllamaTimeout   time.Duration
	DedupeTTL       time.Duration
	QueueSize       int
}

func loadConfig() (config, error) {
	cfg := config{
		ListenAddress:   envOrDefault("LISTEN_ADDRESS", ":8080"),
		OllamaURL:       strings.TrimRight(envOrDefault("OLLAMA_URL", "http://host.docker.internal:11434"), "/"),
		OllamaModel:     envOrDefault("OLLAMA_MODEL", "llama3.2:3b"),
		PrometheusURL:   strings.TrimRight(envOrDefault("PROMETHEUS_URL", "http://central-monitoring-prometheus.monitoring.svc.cluster.local:9090"), "/"),
		SlackWebhookURL: strings.TrimSpace(os.Getenv("SLACK_WEBHOOK_URL")),
		WebhookToken:    strings.TrimSpace(os.Getenv("WEBHOOK_TOKEN")),
		GrafanaURL:      envOrDefault("GRAFANA_URL", "http://localhost:3000/d/multicloud-central-overview/multi-cloud-central-overview"),
		PrometheusUIURL: envOrDefault("PROMETHEUS_UI_URL", "http://localhost:9090/alerts"),
		AlertmanagerURL: envOrDefault("ALERTMANAGER_URL", "http://localhost:9093/#/alerts"),
		ArgoCDURL:       envOrDefault("ARGOCD_URL", "https://localhost:8080/applications/central-monitoring"),
		OllamaTimeout:   envDuration("OLLAMA_TIMEOUT", 120*time.Second),
		DedupeTTL:       envDuration("DEDUPE_TTL", 4*time.Hour),
		QueueSize:       envInt("QUEUE_SIZE", 20),
	}

	var missing []string
	if cfg.SlackWebhookURL == "" {
		missing = append(missing, "SLACK_WEBHOOK_URL")
	}
	if cfg.WebhookToken == "" {
		missing = append(missing, "WEBHOOK_TOKEN")
	}
	if len(missing) > 0 {
		return config{}, fmt.Errorf("required environment variables are missing: %s", strings.Join(missing, ", "))
	}
	if cfg.QueueSize < 1 {
		return config{}, errors.New("QUEUE_SIZE must be greater than zero")
	}
	return cfg, nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		slog.Warn("invalid duration; using default", "variable", name, "value", value, "default", fallback)
		return fallback
	}
	return parsed
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		slog.Warn("invalid integer; using default", "variable", name, "value", value, "default", fallback)
		return fallback
	}
	return parsed
}

type alertmanagerWebhook struct {
	Version           string            `json:"version"`
	GroupKey          string            `json:"groupKey"`
	TruncatedAlerts   int               `json:"truncatedAlerts"`
	Status            string            `json:"status"`
	Receiver          string            `json:"receiver"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Alerts            []alert           `json:"alerts"`
}

type alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

type rcaResult struct {
	IncidentSummary         string   `json:"incident_summary"`
	ProbableCause           string   `json:"probable_cause"`
	Confidence              string   `json:"confidence"`
	Evidence                []string `json:"evidence"`
	Impact                  string   `json:"impact"`
	RecommendedActions      []string `json:"recommended_actions"`
	Limitations             string   `json:"limitations"`
	RequiresHumanValidation bool     `json:"requires_human_validation"`
}

type service struct {
	cfg         config
	promClient  *http.Client
	aiClient    *http.Client
	slackClient *http.Client
	jobs        chan alertmanagerWebhook
	seenMu      sync.Mutex
	seen        map[string]time.Time
}

func newService(cfg config) *service {
	return &service{
		cfg:         cfg,
		promClient:  &http.Client{Timeout: 10 * time.Second},
		aiClient:    &http.Client{Timeout: cfg.OllamaTimeout},
		slackClient: &http.Client{Timeout: 15 * time.Second},
		jobs:        make(chan alertmanagerWebhook, cfg.QueueSize),
		seen:        make(map[string]time.Time),
	}
}

func (s *service) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("POST /api/v1/alerts", s.receiveAlert)
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *service) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
}

func (s *service) ready(w http.ResponseWriter, r *http.Request) {
	checks := []struct {
		name string
		url  string
	}{
		{name: "ollama", url: s.cfg.OllamaURL + "/api/version"},
		{name: "prometheus", url: s.cfg.PrometheusURL + "/-/ready"},
	}
	for _, check := range checks {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, check.url, nil)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not-ready", "dependency": check.name})
			return
		}
		resp, err := s.promClient.Do(req)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not-ready", "dependency": check.name})
			return
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not-ready", "dependency": check.name})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *service) receiveAlert(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r.Header.Get("Authorization")) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxWebhookBody)
	defer r.Body.Close()

	var webhook alertmanagerWebhook
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&webhook); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid Alertmanager webhook payload"})
		return
	}
	if strings.ToLower(webhook.Status) != "firing" {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "reason": "not firing"})
		return
	}
	if len(webhook.Alerts) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "webhook contains no alerts"})
		return
	}

	key := s.dedupeKey(webhook)
	if s.wasRecentlySeen(key) {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "duplicate"})
		return
	}

	select {
	case s.jobs <- webhook:
		s.markSeen(key)
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "RCA queue is full"})
	}
}

func (s *service) authorized(header string) bool {
	expected := "Bearer " + s.cfg.WebhookToken
	return subtle.ConstantTimeCompare([]byte(header), []byte(expected)) == 1
}

func (s *service) dedupeKey(webhook alertmanagerWebhook) string {
	if webhook.GroupKey != "" {
		return webhook.GroupKey
	}
	var fingerprints []string
	for _, item := range webhook.Alerts {
		fingerprints = append(fingerprints, item.Fingerprint)
	}
	sort.Strings(fingerprints)
	return strings.Join(fingerprints, ",")
}

func (s *service) wasRecentlySeen(key string) bool {
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	cutoff := time.Now().Add(-s.cfg.DedupeTTL)
	for existing, timestamp := range s.seen {
		if timestamp.Before(cutoff) {
			delete(s.seen, existing)
		}
	}
	timestamp, found := s.seen[key]
	return found && timestamp.After(cutoff)
}

func (s *service) markSeen(key string) {
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	s.seen[key] = time.Now()
}

func (s *service) runWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case webhook := <-s.jobs:
			if err := s.process(ctx, webhook); err != nil {
				slog.Error("RCA processing failed", "error", err, "group_key", webhook.GroupKey)
			}
		}
	}
}

func (s *service) process(parent context.Context, webhook alertmanagerWebhook) error {
	ctx, cancel := context.WithTimeout(parent, s.cfg.OllamaTimeout+30*time.Second)
	defer cancel()

	labels, annotations := mergedAlertContext(webhook)
	evidence := s.collectEvidence(ctx, labels)

	rca, err := s.generateRCA(ctx, labels, annotations, evidence)
	if err != nil {
		return fmt.Errorf("generate RCA: %w", err)
	}
	if err := s.postSlack(ctx, labels, rca); err != nil {
		return fmt.Errorf("post Slack RCA: %w", err)
	}

	slog.Info(
		"AI-assisted RCA delivered",
		"alertname", labels["alertname"],
		"cluster", labels["cluster"],
		"confidence", rca.Confidence,
	)
	return nil
}

func mergedAlertContext(webhook alertmanagerWebhook) (map[string]string, map[string]string) {
	labels := make(map[string]string)
	annotations := make(map[string]string)
	if len(webhook.Alerts) > 0 {
		for key, value := range webhook.Alerts[0].Labels {
			labels[key] = value
		}
		for key, value := range webhook.Alerts[0].Annotations {
			annotations[key] = value
		}
	}
	for key, value := range webhook.CommonLabels {
		labels[key] = value
	}
	for key, value := range webhook.CommonAnnotations {
		annotations[key] = value
	}
	return labels, annotations
}

type querySpec struct {
	name  string
	query string
}

type prometheusResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []json.RawMessage `json:"value"`
		} `json:"result"`
	} `json:"data"`
	Error string `json:"error"`
}

func (s *service) collectEvidence(ctx context.Context, labels map[string]string) []string {
	cluster := labels["cluster"]
	if cluster == "" {
		return []string{"Prometheus evidence unavailable: alert has no cluster label"}
	}

	quotedCluster := strconv.Quote(cluster)
	queries := []querySpec{
		{
			name:  "unhealthy scrape targets",
			query: fmt.Sprintf(`sum(up{cluster=%s} == bool 0)`, quotedCluster),
		},
		{
			name:  "ready Kubernetes nodes",
			query: fmt.Sprintf(`sum(kube_node_status_condition{cluster=%s,condition="Ready",status="true"})`, quotedCluster),
		},
		{
			name:  "pending or failed pods",
			query: fmt.Sprintf(`sum(kube_pod_status_phase{cluster=%s,phase=~"Pending|Failed|Unknown"})`, quotedCluster),
		},
		{
			name:  "container restarts during the last 15 minutes",
			query: fmt.Sprintf(`sum(increase(kube_pod_container_status_restarts_total{cluster=%s}[15m]))`, quotedCluster),
		},
		{
			name:  "node memory utilization percentage",
			query: fmt.Sprintf(`100 * (1 - sum(node_memory_MemAvailable_bytes{cluster=%s}) / sum(node_memory_MemTotal_bytes{cluster=%s}))`, quotedCluster, quotedCluster),
		},
		{
			name:  "node CPU utilization percentage",
			query: fmt.Sprintf(`100 - avg(rate(node_cpu_seconds_total{cluster=%s,mode="idle"}[5m])) * 100`, quotedCluster),
		},
	}

	evidence := make([]string, 0, len(queries))
	for _, spec := range queries {
		value, err := s.queryPrometheus(ctx, spec.query)
		if err != nil {
			evidence = append(evidence, fmt.Sprintf("%s: unavailable (%s)", spec.name, safeError(err)))
			continue
		}
		evidence = append(evidence, fmt.Sprintf("%s: %s", spec.name, value))
	}
	return evidence
}

func (s *service) queryPrometheus(ctx context.Context, query string) (string, error) {
	endpoint, err := url.Parse(s.cfg.PrometheusURL + "/api/v1/query")
	if err != nil {
		return "", err
	}
	values := endpoint.Query()
	values.Set("query", query)
	endpoint.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := s.promClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var decoded prometheusResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", err
	}
	if decoded.Status != "success" {
		return "", fmt.Errorf("Prometheus query failed: %s", decoded.Error)
	}
	if len(decoded.Data.Result) == 0 {
		return "no data", nil
	}

	var output []string
	for index, result := range decoded.Data.Result {
		if index >= 5 {
			output = append(output, "additional series omitted")
			break
		}
		if len(result.Value) < 2 {
			continue
		}
		var value string
		if err := json.Unmarshal(result.Value[1], &value); err != nil {
			value = string(result.Value[1])
		}
		output = append(output, formatMetric(result.Metric, value))
	}
	if len(output) == 0 {
		return "no data", nil
	}
	return strings.Join(output, "; "), nil
}

func formatMetric(metric map[string]string, value string) string {
	if len(metric) == 0 {
		return value
	}
	keys := make([]string, 0, len(metric))
	for key := range metric {
		if key == "__name__" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var labels []string
	for _, key := range keys {
		labels = append(labels, fmt.Sprintf("%s=%s", key, metric[key]))
	}
	if len(labels) == 0 {
		return value
	}
	return fmt.Sprintf("%s value=%s", strings.Join(labels, ","), value)
}

func safeError(err error) string {
	message := err.Error()
	if len(message) > 120 {
		return message[:120]
	}
	return message
}

func rcaJSONSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"incident_summary": map[string]any{"type": "string"},
			"probable_cause":   map[string]any{"type": "string"},
			"confidence": map[string]any{
				"type": "string",
				"enum": []string{"high", "medium", "low"},
			},
			"evidence": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			"impact": map[string]any{"type": "string"},
			"recommended_actions": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			"limitations":               map[string]any{"type": "string"},
			"requires_human_validation": map[string]any{"type": "boolean"},
		},
		"required": []string{
			"incident_summary",
			"probable_cause",
			"confidence",
			"evidence",
			"impact",
			"recommended_actions",
			"limitations",
			"requires_human_validation",
		},
		"additionalProperties": false,
	}
}

type ollamaRequest struct {
	Model   string         `json:"model"`
	System  string         `json:"system"`
	Prompt  string         `json:"prompt"`
	Format  map[string]any `json:"format"`
	Stream  bool           `json:"stream"`
	Think   bool           `json:"think"`
	Options map[string]any `json:"options"`
}

type ollamaResponse struct {
	Model    string `json:"model"`
	Response string `json:"response"`
	Done     bool   `json:"done"`
	Error    string `json:"error"`
}

func (s *service) generateRCA(
	ctx context.Context,
	labels map[string]string,
	annotations map[string]string,
	evidence []string,
) (rcaResult, error) {
	contextPayload := map[string]any{
		"alert_labels":        labels,
		"alert_annotations":   annotations,
		"prometheus_evidence": evidence,
		"analysis_time_utc":   time.Now().UTC().Format(time.RFC3339),
	}
	contextJSON, err := json.MarshalIndent(contextPayload, "", "  ")
	if err != nil {
		return rcaResult{}, err
	}

	requestPayload := ollamaRequest{
		Model: s.cfg.OllamaModel,
		System: strings.Join([]string{
			"You are an enterprise Kubernetes SRE incident-analysis assistant.",
			"Use only the alert context and observed Prometheus evidence supplied by the caller.",
			"Never invent logs, events, metrics, commands, deployments, or confirmed causes.",
			"Treat the probable cause as a hypothesis unless the evidence directly proves it.",
			"Use low confidence when evidence is missing, contradictory, stale, or says no data.",
			"Recommend read-only investigation before remediation.",
			"Never recommend destructive or autonomous remediation.",
			"Keep each evidence item and action concise enough for a Slack incident card.",
		}, " "),
		Prompt: fmt.Sprintf(
			"Generate an AI-assisted RCA that requires human validation.\n\nINCIDENT CONTEXT\n%s",
			string(contextJSON),
		),
		Format: rcaJSONSchema(),
		Stream: false,
		Think:  false,
		Options: map[string]any{
			"temperature": 0.1,
			"num_ctx":     4096,
		},
	}

	body, err := json.Marshal(requestPayload)
	if err != nil {
		return rcaResult{}, err
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		s.cfg.OllamaURL+"/api/generate",
		bytes.NewReader(body),
	)
	if err != nil {
		return rcaResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.aiClient.Do(req)
	if err != nil {
		return rcaResult{}, err
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return rcaResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return rcaResult{}, fmt.Errorf("Ollama HTTP %d: %s", resp.StatusCode, truncate(string(responseBody), 200))
	}

	var generated ollamaResponse
	if err := json.Unmarshal(responseBody, &generated); err != nil {
		return rcaResult{}, err
	}
	if generated.Error != "" {
		return rcaResult{}, errors.New(generated.Error)
	}
	if !generated.Done {
		return rcaResult{}, errors.New("Ollama response was incomplete")
	}

	var result rcaResult
	if err := json.Unmarshal([]byte(generated.Response), &result); err != nil {
		return rcaResult{}, fmt.Errorf("decode structured RCA: %w", err)
	}
	normalizeRCA(&result)
	return result, nil
}

func normalizeRCA(result *rcaResult) {
	switch strings.ToLower(result.Confidence) {
	case "high", "medium", "low":
		result.Confidence = strings.ToLower(result.Confidence)
	default:
		result.Confidence = "low"
	}
	result.IncidentSummary = truncate(strings.TrimSpace(result.IncidentSummary), 500)
	result.ProbableCause = truncate(strings.TrimSpace(result.ProbableCause), 1000)
	result.Impact = truncate(strings.TrimSpace(result.Impact), 1000)
	result.Limitations = truncate(strings.TrimSpace(result.Limitations), 750)
	result.Evidence = cleanList(result.Evidence, 6, 500)
	result.RecommendedActions = cleanList(result.RecommendedActions, 5, 500)
	result.RequiresHumanValidation = true
}

func cleanList(values []string, limit, maxLength int) []string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		cleaned = append(cleaned, truncate(value, maxLength))
		if len(cleaned) >= limit {
			break
		}
	}
	return cleaned
}

func truncate(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum-1]) + "…"
}

func (s *service) postSlack(ctx context.Context, labels map[string]string, result rcaResult) error {
	payload := s.slackPayload(labels, result)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.SlackWebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.slackClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Slack HTTP %d: %s", resp.StatusCode, truncate(string(responseBody), 200))
	}
	return nil
}

func (s *service) slackPayload(labels map[string]string, result rcaResult) map[string]any {
	alertName := fallback(labels["alertname"], "UnknownAlert")
	cluster := fallback(labels["cluster"], "unknown")
	cloud := strings.ToUpper(fallback(labels["cloud"], "unknown"))
	environment := strings.ToUpper(fallback(labels["environment"], "unknown"))
	confidence := strings.ToUpper(result.Confidence)

	evidence := markdownList(result.Evidence, "No supporting evidence was returned.")
	actions := markdownList(result.RecommendedActions, "Escalate to the platform on-call engineer.")
	color := "#7C3AED"
	switch result.Confidence {
	case "high":
		color = "#2EB67D"
	case "medium":
		color = "#ECB22E"
	case "low":
		color = "#E01E5A"
	}

	blocks := []any{
		map[string]any{
			"type": "header",
			"text": map[string]any{
				"type":  "plain_text",
				"text":  "🤖 AI-Assisted Root Cause Analysis",
				"emoji": true,
			},
		},
		map[string]any{
			"type": "section",
			"fields": []any{
				markdownField("*Alert*\n`" + escapeSlack(alertName) + "`"),
				markdownField("*Confidence*\n" + confidenceEmoji(result.Confidence) + " `" + confidence + "`"),
				markdownField("*Cloud / Cluster*\n" + escapeSlack(cloud) + " / `" + escapeSlack(cluster) + "`"),
				markdownField("*Environment*\n`" + escapeSlack(environment) + "`"),
			},
		},
		map[string]any{"type": "divider"},
		markdownSection("*Probable cause*\n" + escapeSlack(result.ProbableCause)),
		markdownSection("*Observed evidence*\n" + evidence),
		markdownSection("*Potential impact*\n" + escapeSlack(result.Impact)),
		markdownSection("*Recommended investigation*\n" + actions),
		markdownSection("*Limitations*\n" + escapeSlack(result.Limitations)),
	}

	var buttons []any
	addButton := func(text, targetURL, style string) {
		if strings.TrimSpace(targetURL) == "" {
			return
		}
		button := map[string]any{
			"type": "button",
			"text": map[string]any{"type": "plain_text", "text": text, "emoji": true},
			"url":  targetURL,
		}
		if style != "" {
			button["style"] = style
		}
		buttons = append(buttons, button)
	}
	addButton("Open Grafana", s.cfg.GrafanaURL, "primary")
	addButton("Open Prometheus", s.cfg.PrometheusUIURL, "")
	addButton("Open Alertmanager", s.cfg.AlertmanagerURL, "")
	addButton("Open Argo CD", s.cfg.ArgoCDURL, "")
	if len(buttons) > 0 {
		blocks = append(blocks, map[string]any{"type": "actions", "elements": buttons})
	}

	blocks = append(blocks, map[string]any{
		"type": "context",
		"elements": []any{
			map[string]any{
				"type": "mrkdwn",
				"text": "⚠️ *AI-assisted hypothesis — human validation is required before remediation.*",
			},
			map[string]any{
				"type": "mrkdwn",
				"text": "Model: `" + escapeSlack(s.cfg.OllamaModel) + "` • Evidence: central Prometheus",
			},
		},
	})

	return map[string]any{
		"text": fmt.Sprintf("AI-assisted RCA for %s on %s", alertName, cluster),
		"attachments": []any{
			map[string]any{
				"color":  color,
				"blocks": blocks,
			},
		},
	}
}

func markdownField(text string) map[string]any {
	return map[string]any{"type": "mrkdwn", "text": truncate(text, 1900)}
}

func markdownSection(text string) map[string]any {
	return map[string]any{
		"type": "section",
		"text": map[string]any{"type": "mrkdwn", "text": truncate(text, 2900)},
	}
}

func markdownList(values []string, empty string) string {
	if len(values) == 0 {
		return escapeSlack(empty)
	}
	lines := make([]string, 0, len(values))
	for _, value := range values {
		lines = append(lines, "• "+escapeSlack(value))
	}
	return strings.Join(lines, "\n")
}

func confidenceEmoji(confidence string) string {
	switch confidence {
	case "high":
		return "🟢"
	case "medium":
		return "🟠"
	default:
		return "🔴"
	}
}

func escapeSlack(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(value)
}

func fallback(value, replacement string) string {
	if strings.TrimSpace(value) == "" {
		return replacement
	}
	return value
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := loadConfig()
	if err != nil {
		slog.Error("configuration error", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app := newService(cfg)
	go app.runWorker(ctx)

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           app.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		slog.Info("AI RCA service started", "address", cfg.ListenAddress, "model", cfg.OllamaModel)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server failed", "error", err)
			stop()
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP server shutdown failed", "error", err)
	}
	slog.Info("AI RCA service stopped")
}
