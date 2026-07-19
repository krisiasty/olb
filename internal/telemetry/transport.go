package telemetry

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type transport struct {
	next      http.RoundTripper
	collector *Collector
	apiLogger *APILogger
}

func NewTransport(next http.RoundTripper, collector *Collector, apiLoggers ...*APILogger) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	var apiLogger *APILogger
	if len(apiLoggers) > 0 {
		apiLogger = apiLoggers[0]
	}
	return &transport{next: next, collector: collector, apiLogger: apiLogger}
}

func (t *transport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t.collector == nil && t.apiLogger == nil {
		return t.next.RoundTrip(request)
	}
	var generation uint64
	if t.collector != nil {
		generation = t.collector.Begin()
	}
	endpoint := Endpoint(request)
	callID := ""
	if t.apiLogger != nil {
		callID = t.apiLogger.logRequest(request, endpoint)
	}
	started := time.Now()
	response, err := t.next.RoundTrip(request)
	if err != nil {
		duration := time.Since(started)
		outcome := classify(request.Context(), 0, err)
		t.finish(generation, endpoint, callID, request, nil, duration, outcome, nil, err, true)
		return nil, err
	}
	var capture *bodyCapture
	if t.apiLogger != nil && t.apiLogger.includeBodies && !isAuthenticationEndpoint(request) && response.Body != nil {
		capture = newBodyCapture(t.apiLogger.maxBodyBytes)
	}
	finish := func(bodyErr error, complete bool) {
		duration := time.Since(started)
		outcome := classify(request.Context(), response.StatusCode, bodyErr)
		t.finish(generation, endpoint, callID, request, response, duration, outcome, capture, bodyErr, complete)
	}
	if response.Body == nil {
		finish(nil, true)
		return response, nil
	}
	response.Body = &observedBody{ReadCloser: response.Body, capture: capture, finish: finish}
	return response, nil
}

func (t *transport) finish(generation uint64, endpoint, callID string, request *http.Request, response *http.Response, duration time.Duration, outcome Outcome, capture *bodyCapture, responseErr error, complete bool) {
	if t.collector != nil {
		t.collector.Finish(generation, endpoint, duration, outcome)
	}
	if t.apiLogger != nil {
		threshold := DefaultSlowThreshold
		if t.collector != nil {
			threshold = t.collector.slowThreshold
		}
		t.apiLogger.logResponse(request, response, callID, endpoint, duration, outcome, capture, responseErr, complete, duration >= threshold)
	}
}

type observedBody struct {
	io.ReadCloser
	capture *bodyCapture
	once    sync.Once
	finish  func(error, bool)
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.capture.add(p[:n])
	if err != nil {
		finishErr := err
		complete := false
		if errors.Is(err, io.EOF) {
			finishErr = nil
			complete = true
		}
		b.once.Do(func() { b.finish(finishErr, complete) })
	}
	return n, err
}

func (b *observedBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() { b.finish(err, false) })
	return err
}

func classify(ctx context.Context, statusCode int, err error) Outcome {
	if isTimeout(ctx, err) {
		return Timeout
	}
	if err != nil || statusCode >= http.StatusBadRequest {
		return Failure
	}
	return Success
}

func isTimeout(ctx context.Context, err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

var resourceIDPattern = regexp.MustCompile(`(?i)^(?:[0-9a-f]{32}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)

var paginationQueryKeys = map[string]bool{
	"limit": true, "marker": true, "page": true, "sort": true,
	"sort_dir": true, "sort_key": true,
}

// Endpoint returns a stable, non-sensitive endpoint label. Resource/project
// UUIDs and query values are discarded; semantic filter-key names are retained.
func Endpoint(request *http.Request) string {
	if request == nil || request.URL == nil {
		return "UNKNOWN openstack /"
	}
	path := normalizePath(request.URL.EscapedPath())
	service := inferService(path)
	query := normalizedQueryKeys(request.URL.Query())
	method := request.Method
	if method == "" {
		method = "UNKNOWN"
	}
	return method + " " + service + " " + path + query
}

func normalizePath(path string) string {
	if path == "" {
		return "/"
	}
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		decoded, err := url.PathUnescape(segment)
		if err == nil && resourceIDPattern.MatchString(decoded) {
			segments[i] = ":id"
		}
	}
	path = strings.Join(segments, "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func normalizedQueryKeys(values url.Values) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if !paginationQueryKeys[key] {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	return "?" + strings.Join(keys, "&")
}

func inferService(path string) string {
	switch {
	case strings.Contains(path, "/lbaas/"):
		return "octavia"
	case strings.Contains(path, "/secrets") || strings.Contains(path, "/containers"):
		return "barbican"
	case strings.Contains(path, "/floatingips") || strings.Contains(path, "/ports"):
		return "neutron"
	case strings.Contains(path, "/servers"):
		return "nova"
	case strings.Contains(path, "/auth/") || strings.Contains(path, "/projects"):
		return "keystone"
	default:
		return "openstack"
	}
}
