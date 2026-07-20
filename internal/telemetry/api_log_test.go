package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAPILoggerWritesSanitizedCorrelatedTransactions(t *testing.T) {
	path := t.TempDir() + "/api.jsonl"
	logger, err := OpenAPILogger(path, APILogOptions{IncludeBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	fixedTime := time.Date(2026, time.July, 19, 11, 20, 45, 123_000_000, time.UTC)
	logger.now = func() time.Time { return fixedTime }

	collector := NewCollector(time.Nanosecond)
	client := http.Client{Transport: NewTransport(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestBody, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if got, want := string(requestBody), `{"loadbalancer":{"name":"public","password":"request-secret"}}`; got != want {
			t.Fatalf("transport received request body %q, want %q", got, want)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Header: http.Header{
				"Content-Type":           []string{"application/json"},
				"X-Subject-Token":        []string{"response-header-secret"},
				"X-Openstack-Request-Id": []string{"req-123"},
			},
			Body:    io.NopCloser(strings.NewReader(`{"loadbalancer":{"name":"public","token":"response-secret"}}`)),
			Request: request,
		}, nil
	}), collector, logger)}

	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"https://api.example/v2/lbaas/loadbalancers?project_id=public-project&token=query-secret",
		strings.NewReader(`{"loadbalancer":{"name":"public","password":"request-secret"}}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer request-header-secret")
	request.Header.Set("X-Debug", "visible")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err = response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if got, want := string(responseBody), `{"loadbalancer":{"name":"public","token":"response-secret"}}`; got != want {
		t.Fatalf("caller received response body %q, want %q", got, want)
	}
	if err = logger.Close(); err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("API log permissions = %o, want 600", got)
		}
	}

	// #nosec G304 -- path is an isolated t.TempDir test fixture.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{
		"request-secret", "response-secret", "query-secret",
		"request-header-secret", "response-header-secret",
	} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("API log leaked %q:\n%s", secret, raw)
		}
	}
	events := decodeAPILogEvents(t, raw)
	if len(events) != 2 {
		t.Fatalf("API log events = %d, want request and response", len(events))
	}
	requestEvent, responseEvent := events[0], events[1]
	if requestEvent.Event != "request" || responseEvent.Event != "response" ||
		requestEvent.CallID == "" || responseEvent.CallID != requestEvent.CallID {
		t.Fatalf("uncorrelated API log events: %+v %+v", requestEvent, responseEvent)
	}
	if requestEvent.Timestamp != "2026-07-19T11:20:45.123Z" || requestEvent.Service != "octavia" ||
		requestEvent.Endpoint != "POST octavia /v2/lbaas/loadbalancers?project_id&token" {
		t.Fatalf("request metadata = %+v", requestEvent)
	}
	if !strings.Contains(requestEvent.URL, "project_id=public-project") || !strings.Contains(requestEvent.URL, "token=%5BREDACTED%5D") {
		t.Fatalf("sanitized request URL = %q", requestEvent.URL)
	}
	if got := requestEvent.Headers["Authorization"]; len(got) != 1 || got[0] != "[REDACTED]" {
		t.Fatalf("authorization header = %v", got)
	}
	if got := requestEvent.Headers["X-Debug"]; len(got) != 1 || got[0] != "visible" {
		t.Fatalf("non-sensitive header = %v", got)
	}
	assertJSONBodyRedacted(t, requestEvent.Body, "loadbalancer", "password")
	assertJSONBodyRedacted(t, responseEvent.Body, "loadbalancer", "token")
	if responseEvent.Status == nil || *responseEvent.Status != http.StatusCreated || responseEvent.Outcome != "success" ||
		responseEvent.Slow == nil || !*responseEvent.Slow || responseEvent.DurationMS == nil {
		t.Fatalf("response metadata = %+v", responseEvent)
	}
	if got := responseEvent.Headers["X-Subject-Token"]; len(got) != 1 || got[0] != "[REDACTED]" {
		t.Fatalf("response token header = %v", got)
	}
}

func TestAPILoggerWritesMonotonicTimestamps(t *testing.T) {
	path := t.TempDir() + "/api.jsonl"
	logger, err := OpenAPILogger(path, APILogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	times := []time.Time{base, base.Add(-time.Second), base}
	logger.now = func() time.Time {
		next := times[0]
		times = times[1:]
		return next
	}
	for _, callID := range []string{"first", "second", "third"} {
		logger.record(apiLogEvent{CallID: callID, Event: "request"})
	}
	if err = logger.Close(); err != nil {
		t.Fatal(err)
	}

	// #nosec G304 -- path is an isolated t.TempDir test fixture.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	events := decodeAPILogEvents(t, raw)
	if len(events) != 3 {
		t.Fatalf("API log events = %d, want 3", len(events))
	}
	var previous time.Time
	for i, event := range events {
		timestamp, parseErr := time.Parse(time.RFC3339Nano, event.Timestamp)
		if parseErr != nil {
			t.Fatalf("event %d timestamp %q: %v", i, event.Timestamp, parseErr)
		}
		if i > 0 && !timestamp.After(previous) {
			t.Fatalf("event %d timestamp %s is not after %s", i, timestamp, previous)
		}
		previous = timestamp
	}
}

func TestAPILoggerLabelsBarbicanService(t *testing.T) {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://key-manager.example/v1/secrets/123e4567-e89b-12d3-a456-426614174000/payload", nil)
	if err != nil {
		t.Fatal(err)
	}
	logger := &APILogger{now: func() time.Time { return time.Time{} }}
	event := logger.baseEvent(request, "call-id", "response", Endpoint(request))
	if event.Service != "barbican" || event.Endpoint != "GET barbican /v1/secrets/:id/payload" {
		t.Fatalf("Barbican API log metadata = %+v", event)
	}
}

func TestEndpointLabelsOctaviaAdministrativePath(t *testing.T) {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://load-balancer.example/v2.0/octavia/amphorae?loadbalancer_id=123e4567-e89b-12d3-a456-426614174000", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := Endpoint(request), "GET octavia /v2.0/octavia/amphorae?loadbalancer_id"; got != want {
		t.Fatalf("administrative Octavia endpoint = %q, want %q", got, want)
	}
}

func TestAPILoggerSuppressesAuthenticationBodiesAndClassifiesTimeouts(t *testing.T) {
	path := t.TempDir() + "/api.jsonl"
	logger, err := OpenAPILogger(path, APILogOptions{IncludeBodies: true})
	if err != nil {
		t.Fatal(err)
	}
	collector := NewCollector(time.Hour)
	client := http.Client{Transport: NewTransport(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "timeout") {
			return nil, context.DeadlineExceeded
		}
		status := http.StatusCreated
		if strings.Contains(request.URL.Path, "error") {
			status = http.StatusInternalServerError
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"X-Subject-Token": []string{"auth-response-token"}},
			Body:       io.NopCloser(strings.NewReader(`{"password":"auth-response-secret"}`)),
			Request:    request,
		}, nil
	}), collector, logger)}

	authRequest, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://identity.example/v3/auth/tokens",
		strings.NewReader(`{"password":"auth-request-secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	authResponse, err := client.Do(authRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadAll(authResponse.Body); err != nil {
		t.Fatal(err)
	}
	if err = authResponse.Body.Close(); err != nil {
		t.Fatal(err)
	}

	timeoutRequest, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.example/v2/lbaas/timeout?password=timeout-query-secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Do(timeoutRequest); err == nil {
		t.Fatal("timeout request unexpectedly succeeded")
	}
	errorRequest, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.example/v2/lbaas/error", nil)
	if err != nil {
		t.Fatal(err)
	}
	errorResponse, err := client.Do(errorRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadAll(errorResponse.Body); err != nil {
		t.Fatal(err)
	}
	if err = errorResponse.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if err = logger.Close(); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- path is an isolated t.TempDir test fixture.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"auth-request-secret", "auth-response-secret", "auth-response-token", "timeout-query-secret"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("API log leaked %q:\n%s", secret, raw)
		}
	}
	events := decodeAPILogEvents(t, raw)
	if len(events) != 6 {
		t.Fatalf("API log events = %d, want 6", len(events))
	}
	for _, event := range events[:2] {
		if event.Body != nil || event.BodyOmitted != "authentication endpoint" {
			t.Errorf("authentication body was not suppressed: %+v", event)
		}
	}
	timeout := events[3]
	if timeout.Event != "response" || timeout.Outcome != "timeout" || timeout.Status != nil || timeout.Error == "" ||
		timeout.Slow == nil || *timeout.Slow {
		t.Fatalf("timeout event = %+v", timeout)
	}
	failure := events[5]
	if failure.Event != "response" || failure.Outcome != "error" || failure.Status == nil ||
		*failure.Status != http.StatusInternalServerError {
		t.Fatalf("error response event = %+v", failure)
	}
}

func TestAPILoggerDefaultsToMetadataOnly(t *testing.T) {
	path := t.TempDir() + "/api.jsonl"
	logger, err := OpenAPILogger(path, APILogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client := http.Client{Transport: NewTransport(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"safe":"response-body-must-not-appear"}`)),
			Request:    request,
		}, nil
	}), nil, logger)}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://api.example/v2/lbaas/loadbalancers",
		strings.NewReader(`{"safe":"request-body-must-not-appear"}`))
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
	if err = response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if err = logger.Close(); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- path is an isolated t.TempDir test fixture.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "body-must-not-appear") {
		t.Fatalf("metadata-only API log included a body:\n%s", raw)
	}
	for _, event := range decodeAPILogEvents(t, raw) {
		if event.Body != nil || event.BodyOmitted != "" {
			t.Errorf("metadata-only event contains body fields: %+v", event)
		}
	}
}

func TestAPILogSensitiveNamesAndLegacyTokenEndpoint(t *testing.T) {
	for _, name := range []string{"Authorization", "X-Auth-Token", "application_credential", "api_key", "ssh_key", "key"} {
		if !sensitiveName(name) {
			t.Errorf("%q should be sensitive", name)
		}
	}
	if sensitiveName("monkey") {
		t.Fatal("unrelated field name should not be redacted")
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://identity.example/v2.0/tokens", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !isAuthenticationEndpoint(request) {
		t.Fatal("legacy Keystone token endpoint should suppress bodies")
	}
}

func TestAPILoggerOmitsOversizedAndIncompleteBodies(t *testing.T) {
	path := t.TempDir() + "/api.jsonl"
	logger, err := OpenAPILogger(path, APILogOptions{IncludeBodies: true, MaxBodyBytes: 16})
	if err != nil {
		t.Fatal(err)
	}
	client := http.Client{Transport: NewTransport(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `{"value":"this response is deliberately too long"}`
		if strings.Contains(request.URL.Path, "incomplete") {
			body = `{"ok":true}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	}), NewCollector(time.Second), logger)}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example/v2/lbaas/loadbalancers", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
	if err = response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	incompleteRequest, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example/v2/lbaas/incomplete", nil)
	if err != nil {
		t.Fatal(err)
	}
	incompleteResponse, err := client.Do(incompleteRequest)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1)
	if _, err = incompleteResponse.Body.Read(buffer); err != nil {
		t.Fatal(err)
	}
	if err = incompleteResponse.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if err = logger.Close(); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- path is an isolated t.TempDir test fixture.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	events := decodeAPILogEvents(t, raw)
	if len(events) != 4 {
		t.Fatalf("API log events = %d, want 4", len(events))
	}
	responseEvent := events[1]
	if responseEvent.Body != nil || !responseEvent.BodyTruncated || !strings.Contains(responseEvent.BodyOmitted, "16-byte") {
		t.Fatalf("oversized response body = %+v", responseEvent)
	}
	incompleteEvent := events[3]
	if incompleteEvent.Body != nil || !incompleteEvent.BodyIncomplete || incompleteEvent.BodyOmitted != "response body was not fully read" {
		t.Fatalf("incomplete response body = %+v", incompleteEvent)
	}
}

func decodeAPILogEvents(t *testing.T, data []byte) []apiLogEvent {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	var events []apiLogEvent
	for {
		var event apiLogEvent
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				return events
			}
			t.Fatal(err)
		}
		events = append(events, event)
	}
}

func assertJSONBodyRedacted(t *testing.T, body any, object, field string) {
	t.Helper()
	outer, ok := body.(map[string]any)
	if !ok {
		t.Fatalf("body type = %T, want JSON object", body)
	}
	inner, ok := outer[object].(map[string]any)
	if !ok || inner[field] != "[REDACTED]" || inner["name"] != "public" {
		t.Fatalf("sanitized body = %#v", body)
	}
}
