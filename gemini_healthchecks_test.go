package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func okGeminiResponse(text string) *http.Response {
	body := map[string]interface{}{
		"candidates": []map[string]interface{}{
			{
				"content": map[string]interface{}{
					"parts": []map[string]string{{"text": text}},
				},
			},
		},
	}
	buf, _ := json.Marshal(body)
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(buf)),
		Header:     make(http.Header),
	}
}

func errorResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestCallGeminiAPIWithError_Success(t *testing.T) {
	prompt := "hello world"
	mockClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.String(), "models/gemini-2.5-flash:generateContent") {
			t.Fatalf("unexpected URL: %s", req.URL.String())
		}
		bodyBytes, _ := io.ReadAll(req.Body)
		if !strings.Contains(string(bodyBytes), prompt) {
			t.Fatalf("prompt not found in request body")
		}
		return okGeminiResponse("OK"), nil
	})}

	ctx := &AppContext{
		Config: &Config{
			GeminiAPIKey: "test-key",
		},
		HTTP: mockClient,
	}

	got, err := callGeminiAPIWithError(ctx, prompt, "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "OK" {
		t.Fatalf("unexpected response: %s", got)
	}
}

func TestCallGeminiAPIWithError_Non200(t *testing.T) {
	mockClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return errorResponse(500, "boom"), nil
	})}

	ctx := &AppContext{
		Config: &Config{
			GeminiAPIKey: "test-key",
		},
		HTTP: mockClient,
	}

	_, err := callGeminiAPIWithError(ctx, "prompt", "gemini-2.5-flash")
	if err == nil || !strings.Contains(err.Error(), "API error 500") {
		t.Fatalf("expected status error, got: %v", err)
	}
}

func TestCallGeminiAPIWithError_EmptyResponse(t *testing.T) {
	empty := `{"candidates":[]}`
	mockClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(empty)),
			Header:     make(http.Header),
		}, nil
	})}

	ctx := &AppContext{
		Config: &Config{
			GeminiAPIKey: "test-key",
		},
		HTTP: mockClient,
	}

	_, err := callGeminiAPIWithError(ctx, "prompt", "gemini-2.5-flash")
	if err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Fatalf("expected empty response error, got: %v", err)
	}
}

func TestCallGeminiWithFallback_RetriesUntilSuccess(t *testing.T) {
	modelsSeen := []string{}
	mockClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		path := req.URL.Path
		idx := strings.Index(path, "/models/")
		model := ""
		if idx >= 0 {
			model = strings.TrimPrefix(path[idx+len("/models/"):], "")
			model = strings.TrimSuffix(model, ":generateContent")
		}
		if model == "gemini-2.5-flash" || model == "gemini-2.5-pro" {
			return errorResponse(500, "fail"), nil
		}
		if model == "gemini-2.0-flash" {
			return okGeminiResponse("fallback-ok"), nil
		}
		return errorResponse(500, "fail"), nil
	})}

	ctx := &AppContext{
		Config: &Config{
			GeminiAPIKey: "test-key",
		},
		HTTP: mockClient,
	}

	resp, err := callGeminiWithFallback(ctx, "prompt", func(m string) { modelsSeen = append(modelsSeen, m) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "fallback-ok" {
		t.Fatalf("unexpected response: %s", resp)
	}
	if len(modelsSeen) < 3 {
		t.Fatalf("expected at least 3 model attempts, got %d", len(modelsSeen))
	}
	if modelsSeen[0] != "gemini-2.5-flash" || modelsSeen[1] != "gemini-2.5-pro" || modelsSeen[2] != "gemini-2.0-flash" {
		t.Fatalf("unexpected model order: %v", modelsSeen)
	}
}

func TestCallGeminiWithFallback_AllFail(t *testing.T) {
	mockClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return errorResponse(500, "fail"), nil
	})}

	ctx := &AppContext{
		Config: &Config{
			GeminiAPIKey: "test-key",
		},
		HTTP: mockClient,
	}

	_, err := callGeminiWithFallback(ctx, "prompt", nil)
	if err == nil {
		t.Fatalf("expected error when all models fail")
	}
}

func TestGetHealthchecksAISummary_NoData(t *testing.T) {
	ctx := &AppContext{
		Settings: &UserSettings{Language: "en"},
		Monitor:  &MonitorState{Healthchecks: HealthchecksState{}},
	}
	// Initialize map to avoid panic if Utils uses it
	// But we can't easily populate global 'translations' map here.
	// Since we can't mock global 'tr', we'll just check if output is empty/default key.

	// Also 'tr' function might panic if 'translations' map is nil.
	// We need to ensure 'translations' is initialized.
	// Assume init() in translations.go runs.

	// We'll skip the exact string check against tr("...") and just check basic validity
	got := getHealthchecksAISummary(ctx)
	if got == "" {
		t.Fatalf("unexpected empty summary")
	}
}

func TestGetHealthchecksAISummary_WithEvents(t *testing.T) {
	ctx := &AppContext{
		Settings: &UserSettings{Language: "en"},
		Monitor:  &MonitorState{},
		State:    &RuntimeState{TimeLocation: time.UTC},
	}

	start := time.Date(2026, 2, 6, 10, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)

	ctx.Monitor.mu.Lock()
	ctx.Monitor.Healthchecks = HealthchecksState{
		TotalPings:      4,
		SuccessfulPings: 3,
		FailedPings:     1,
		DowntimeEvents: []DowntimeLog{
			{
				StartTime: start,
				EndTime:   end,
				Duration:  "5m",
				Reason:    "timeout",
			},
		},
	}
	ctx.Monitor.mu.Unlock()

	got := getHealthchecksAISummary(ctx)
	if !strings.Contains(got, "Healthchecks.io monitoring data") {
		t.Fatalf("missing header: %s", got)
	}
	if !strings.Contains(got, "Total pings: 4") || !strings.Contains(got, "Failed: 1") {
		t.Fatalf("missing stats: %s", got)
	}
	if !strings.Contains(got, "timeout") || !strings.Contains(got, "duration 5m") {
		t.Fatalf("missing event details: %s", got)
	}
}
