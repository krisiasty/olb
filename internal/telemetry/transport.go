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
}

func NewTransport(next http.RoundTripper, collector *Collector) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return &transport{next: next, collector: collector}
}

func (t *transport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t.collector == nil {
		return t.next.RoundTrip(request)
	}
	generation := t.collector.Begin()
	endpoint := Endpoint(request)
	started := time.Now()
	response, err := t.next.RoundTrip(request)
	if err != nil {
		t.collector.Finish(generation, endpoint, time.Since(started), classify(request.Context(), 0, err))
		return nil, err
	}
	finish := func(bodyErr error) {
		t.collector.Finish(generation, endpoint, time.Since(started), classify(request.Context(), response.StatusCode, bodyErr))
	}
	if response.Body == nil {
		finish(nil)
		return response, nil
	}
	response.Body = &observedBody{ReadCloser: response.Body, finish: finish}
	return response, nil
}

type observedBody struct {
	io.ReadCloser
	once   sync.Once
	finish func(error)
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		finishErr := err
		if errors.Is(err, io.EOF) {
			finishErr = nil
		}
		b.once.Do(func() { b.finish(finishErr) })
	}
	return n, err
}

func (b *observedBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() { b.finish(err) })
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
