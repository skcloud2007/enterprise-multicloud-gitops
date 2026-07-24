package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testConfig() config {
	return config{
		ListenAddress:   ":8080",
		OllamaURL:       "http://ollama.invalid",
		OllamaModel:     "llama3.2:3b",
		PrometheusURL:   "http://prometheus.invalid",
		SlackWebhookURL: "http://slack.invalid",
		WebhookToken:    "test-token",
		GrafanaURL:      "http://localhost:3000",
		PrometheusUIURL: "http://localhost:9090",
		AlertmanagerURL: "http://localhost:9093",
		ArgoCDURL:       "https://localhost:8080",
		OllamaTimeout:   5 * time.Second,
		DedupeTTL:       time.Hour,
		QueueSize:       2,
	}
}

func firingPayload() string {
	return `{
		"version":"4",
		"groupKey":"test-group",
		"truncatedAlerts":0,
		"status":"firing",
		"receiver":"slack-skm-alerts",
		"groupLabels":{"alertname":"TestAlert"},
		"commonLabels":{
			"alertname":"TestAlert",
			"team":"platform",
			"cluster":"eks-dev",
			"cloud":"aws",
			"environment":"dev",
			"severity":"warning"
		},
		"commonAnnotations":{"summary":"test summary","description":"test description"},
		"externalURL":"http://alertmanager",
		"alerts":[{
			"status":"firing",
			"labels":{"alertname":"TestAlert","cluster":"eks-dev"},
			"annotations":{"summary":"test summary"},
			"startsAt":"2026-07-24T00:00:00Z",
			"endsAt":"2026-07-24T01:00:00Z",
			"generatorURL":"http://prometheus",
			"fingerprint":"abc123"
		}]
	}`
}

func TestReceiveAlertRequiresAuthorization(t *testing.T) {
	app := newService(testConfig())
	request := httptest.NewRequest(http.MethodPost, "/api/v1/alerts", strings.NewReader(firingPayload()))
	response := httptest.NewRecorder()

	app.routes().ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected %d, got %d", http.StatusUnauthorized, response.Code)
	}
}

func TestReceiveAlertQueuesFiringAlert(t *testing.T) {
	app := newService(testConfig())
	request := httptest.NewRequest(http.MethodPost, "/api/v1/alerts", strings.NewReader(firingPayload()))
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()

	app.routes().ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("expected %d, got %d: %s", http.StatusAccepted, response.Code, response.Body.String())
	}
	if len(app.jobs) != 1 {
		t.Fatalf("expected one queued job, got %d", len(app.jobs))
	}
}

func TestReceiveAlertDeduplicatesGroup(t *testing.T) {
	app := newService(testConfig())
	for attempt := 0; attempt < 2; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/alerts", strings.NewReader(firingPayload()))
		request.Header.Set("Authorization", "Bearer test-token")
		response := httptest.NewRecorder()
		app.routes().ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("attempt %d: expected %d, got %d", attempt, http.StatusAccepted, response.Code)
		}
	}
	if len(app.jobs) != 1 {
		t.Fatalf("expected duplicate to be suppressed; queue length=%d", len(app.jobs))
	}
}

func TestGenerateRCAUsesStructuredResponse(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var request map[string]any
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		if request["format"] == nil {
			t.Fatal("structured output schema was not supplied")
		}

		result := `{
			"incident_summary":"Test incident",
			"probable_cause":"Node memory pressure",
			"confidence":"high",
			"evidence":["memory utilization 98%"],
			"impact":"Pods cannot schedule",
			"recommended_actions":["Inspect node memory consumers"],
			"limitations":"No Kubernetes events were supplied",
			"requires_human_validation":true
		}`
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":    "llama3.2:3b",
			"response": result,
			"done":     true,
		})
	}))
	defer ollama.Close()

	cfg := testConfig()
	cfg.OllamaURL = ollama.URL
	app := newService(cfg)
	result, err := app.generateRCA(
		t.Context(),
		map[string]string{"alertname": "TestAlert", "cluster": "eks-dev"},
		map[string]string{"summary": "test"},
		[]string{"memory utilization 98%"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Confidence != "high" {
		t.Fatalf("expected high confidence, got %q", result.Confidence)
	}
	if !result.RequiresHumanValidation {
		t.Fatal("human validation must always be required")
	}
}

func TestPostSlackSendsBlockKitPayload(t *testing.T) {
	var received map[string]any
	slack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer slack.Close()

	cfg := testConfig()
	cfg.SlackWebhookURL = slack.URL
	app := newService(cfg)

	err := app.postSlack(t.Context(), map[string]string{
		"alertname":   "TestAlert",
		"cluster":     "eks-dev",
		"cloud":       "aws",
		"environment": "dev",
	}, rcaResult{
		IncidentSummary:         "Test",
		ProbableCause:           "Memory pressure",
		Confidence:              "medium",
		Evidence:                []string{"memory 95%"},
		Impact:                  "Pods pending",
		RecommendedActions:      []string{"Inspect memory"},
		Limitations:             "Metrics only",
		RequiresHumanValidation: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if received["attachments"] == nil {
		t.Fatal("Slack Block Kit attachment was not generated")
	}
	if !strings.Contains(received["text"].(string), "TestAlert") {
		t.Fatalf("unexpected Slack fallback text: %v", received["text"])
	}
}
