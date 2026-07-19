package telemetry

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCollectorSnapshotOutcomesLatencyAndResetGeneration(t *testing.T) {
	collector := NewCollector(time.Second)
	collector.Observe("GET octavia /v2/lbaas/loadbalancers", 100*time.Millisecond, Success)
	collector.Observe("GET octavia /v2/lbaas/loadbalancers", 2*time.Second, Timeout)
	collector.Observe("GET neutron /v2.0/floatingips", 500*time.Millisecond, Failure)

	snapshot := collector.Snapshot()
	if snapshot.Calls != 3 || snapshot.Successes != 1 || snapshot.Slow != 1 || snapshot.Timeouts != 1 || snapshot.Errors != 1 {
		t.Fatalf("snapshot totals = %+v", snapshot)
	}
	if len(snapshot.Endpoints) != 2 {
		t.Fatalf("endpoint count = %d, want 2", len(snapshot.Endpoints))
	}
	stats := snapshot.Endpoints[0]
	if stats.Endpoint != "GET octavia /v2/lbaas/loadbalancers" || stats.Calls != 2 {
		t.Fatalf("first endpoint = %+v", stats)
	}
	if stats.Min != 100*time.Millisecond || stats.Max != 2*time.Second || stats.Average != 1050*time.Millisecond ||
		stats.Median != 1050*time.Millisecond || stats.P95 != 2*time.Second || stats.P99 != 2*time.Second {
		t.Fatalf("latency stats = %+v", stats)
	}

	oldGeneration := collector.Begin()
	collector.Reset()
	collector.Finish(oldGeneration, "GET old /ignored", time.Second, Failure)
	snapshot = collector.Snapshot()
	if snapshot.Calls != 0 || len(snapshot.Endpoints) != 0 {
		t.Fatalf("reset retained an old in-flight observation: %+v", snapshot)
	}
}

func TestEndpointNormalizesIDsAndQueryValues(t *testing.T) {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.example/v2/lbaas/loadbalancers/123e4567-e89b-12d3-a456-426614174000/stats?marker=secret&limit=20&project_id=also-secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := Endpoint(request), "GET octavia /v2/lbaas/loadbalancers/:id/stats?project_id"; got != want {
		t.Fatalf("Endpoint = %q, want %q", got, want)
	}
}

func TestEndpointIdentifiesBarbicanResources(t *testing.T) {
	for _, test := range []struct {
		url  string
		want string
	}{
		{
			url:  "https://key-manager.example/v1/secrets/123e4567-e89b-12d3-a456-426614174000/payload",
			want: "GET barbican /v1/secrets/:id/payload",
		},
		{
			url:  "https://key-manager.example/v1/containers/123e4567-e89b-12d3-a456-426614174000",
			want: "GET barbican /v1/containers/:id",
		},
	} {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, test.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := Endpoint(request); got != test.want {
			t.Errorf("Endpoint(%q) = %q, want %q", test.url, got, test.want)
		}
	}
}

func TestTransportRecordsCompletedBodiesAndTimeouts(t *testing.T) {
	collector := NewCollector(time.Hour)
	client := http.Client{Transport: NewTransport(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "timeout") {
			return nil, context.DeadlineExceeded
		}
		status := http.StatusOK
		if strings.Contains(request.URL.Path, "error") {
			status = http.StatusInternalServerError
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader("{}")),
			Header:     make(http.Header),
			Request:    request,
		}, nil
	}), collector)}

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example/v2/lbaas/loadbalancers", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot := collector.Snapshot(); snapshot.Calls != 0 {
		t.Fatalf("request was recorded before its response body completed: %+v", snapshot)
	}
	if _, err = io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
	if err = response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	request, err = http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example/v2/lbaas/timeout", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Do(request); err == nil {
		t.Fatal("timeout request unexpectedly succeeded")
	}
	request, err = http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example/v2/lbaas/error", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
	if err = response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot := collector.Snapshot()
	if snapshot.Calls != 3 || snapshot.Successes != 1 || snapshot.Timeouts != 1 || snapshot.Errors != 1 {
		t.Fatalf("transport outcomes = %+v", snapshot)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
