package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultAPILogBodyLimit is the maximum request or response body retained for
// sanitization when explicit body logging is enabled.
const DefaultAPILogBodyLimit int64 = 64 << 10

// APILogOptions controls the explicitly enabled body-capture behavior.
type APILogOptions struct {
	IncludeBodies bool
	MaxBodyBytes  int64
}

// APILogger writes append-only JSON Lines records for OpenStack HTTP requests
// and their matching responses. Writes are serialized because one ProviderClient
// can issue requests concurrently from the TUI's refresh commands.
type APILogger struct {
	mu            sync.Mutex
	file          *os.File
	encoder       *json.Encoder
	includeBodies bool
	maxBodyBytes  int64
	now           func() time.Time
	lastTimestamp time.Time
	writeErr      error
	closed        bool
}

// OpenAPILogger opens path in append mode and forces owner-only permissions.
func OpenAPILogger(path string, options APILogOptions) (*APILogger, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("API log path is empty")
	}
	limit := options.MaxBodyBytes
	if limit <= 0 {
		limit = DefaultAPILogBodyLimit
	}
	// #nosec G304 -- path is supplied explicitly by the user through --api-log.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err = file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("setting owner-only permissions: %w", err)
	}
	return &APILogger{
		file:          file,
		encoder:       json.NewEncoder(file),
		includeBodies: options.IncludeBodies,
		maxBodyBytes:  limit,
		now:           time.Now,
	}, nil
}

// Close flushes the file and reports any earlier asynchronous write failure.
func (l *APILogger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return l.writeErr
	}
	l.closed = true
	syncErr := l.file.Sync()
	closeErr := l.file.Close()
	return errors.Join(l.writeErr, syncErr, closeErr)
}

type apiLogEvent struct {
	Timestamp      string              `json:"timestamp"`
	CallID         string              `json:"call_id"`
	Event          string              `json:"event"`
	Endpoint       string              `json:"endpoint"`
	Service        string              `json:"service"`
	Method         string              `json:"method"`
	URL            string              `json:"url,omitempty"`
	Status         *int                `json:"status,omitempty"`
	DurationMS     *float64            `json:"duration_ms,omitempty"`
	Outcome        string              `json:"outcome,omitempty"`
	Slow           *bool               `json:"slow,omitempty"`
	Error          string              `json:"error,omitempty"`
	Headers        map[string][]string `json:"headers,omitempty"`
	Body           any                 `json:"body,omitempty"`
	BodyOmitted    string              `json:"body_omitted,omitempty"`
	BodyTruncated  bool                `json:"body_truncated,omitempty"`
	BodyIncomplete bool                `json:"body_incomplete,omitempty"`
}

type apiLogBody struct {
	value      any
	omitted    string
	truncated  bool
	incomplete bool
}

func (l *APILogger) logRequest(request *http.Request, endpoint string) string {
	callID := newAPICallID()
	event := l.baseEvent(request, callID, "request", endpoint)
	if request != nil {
		event.URL = sanitizedURL(request.URL)
		event.Headers = sanitizedHeaders(request.Header)
		if l.includeBodies {
			event.applyBody(l.requestBody(request))
		}
	}
	l.record(event)
	return callID
}

func (l *APILogger) logResponse(request *http.Request, response *http.Response, callID, endpoint string, duration time.Duration, outcome Outcome, capture *bodyCapture, responseErr error, complete bool, slow bool) {
	event := l.baseEvent(request, callID, "response", endpoint)
	durationMS := float64(duration) / float64(time.Millisecond)
	event.DurationMS = &durationMS
	event.Outcome = outcomeName(outcome)
	event.Slow = &slow
	if response != nil {
		status := response.StatusCode
		event.Status = &status
		event.Headers = sanitizedHeaders(response.Header)
	}
	if responseErr != nil {
		event.Error = sanitizedError(responseErr)
	}
	if l.includeBodies {
		event.applyBody(l.responseBody(request, capture, complete))
	}
	l.record(event)
}

func (l *APILogger) baseEvent(request *http.Request, callID, event, endpoint string) apiLogEvent {
	method := "UNKNOWN"
	service := "openstack"
	if request != nil {
		if request.Method != "" {
			method = request.Method
		}
		if request.URL != nil {
			service = inferService(request.URL.EscapedPath())
		}
	}
	return apiLogEvent{
		CallID: callID, Event: event, Endpoint: endpoint,
		Service: service, Method: method,
	}
}

func (l *APILogger) record(event apiLogEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.writeErr != nil {
		return
	}
	// Timestamp under the same lock as Encode so concurrent body sanitization
	// cannot produce JSONL records whose timestamps run backwards. The one
	// nanosecond bump also keeps ordering stable if the wall clock is adjusted
	// backwards or a test clock returns the same instant repeatedly.
	timestamp := l.now().UTC()
	if !timestamp.After(l.lastTimestamp) {
		timestamp = l.lastTimestamp.Add(time.Nanosecond)
	}
	l.lastTimestamp = timestamp
	event.Timestamp = timestamp.Format(time.RFC3339Nano)
	if err := l.encoder.Encode(event); err != nil {
		l.writeErr = fmt.Errorf("writing API log: %w", err)
	}
}

func (e *apiLogEvent) applyBody(body apiLogBody) {
	e.Body = body.value
	e.BodyOmitted = body.omitted
	e.BodyTruncated = body.truncated
	e.BodyIncomplete = body.incomplete
}

func (l *APILogger) requestBody(request *http.Request) apiLogBody {
	if request == nil || request.Body == nil || request.ContentLength == 0 {
		return apiLogBody{}
	}
	if isAuthenticationEndpoint(request) {
		return apiLogBody{omitted: "authentication endpoint"}
	}
	if request.GetBody == nil {
		return apiLogBody{omitted: "request body is not replayable"}
	}
	body, err := request.GetBody()
	if err != nil {
		return apiLogBody{omitted: "request body could not be copied"}
	}
	data, truncated, err := readLimited(body, l.maxBodyBytes)
	closeErr := body.Close()
	if err != nil {
		return apiLogBody{omitted: "request body could not be read"}
	}
	if closeErr != nil {
		return apiLogBody{omitted: "request body copy could not be closed"}
	}
	return sanitizedJSONBody(data, truncated, false, l.maxBodyBytes)
}

func (l *APILogger) responseBody(request *http.Request, capture *bodyCapture, complete bool) apiLogBody {
	if isAuthenticationEndpoint(request) {
		return apiLogBody{omitted: "authentication endpoint"}
	}
	if capture == nil || capture.size == 0 {
		return apiLogBody{}
	}
	return sanitizedJSONBody(capture.data, capture.truncated, !complete, l.maxBodyBytes)
}

func sanitizedJSONBody(data []byte, truncated, incomplete bool, limit int64) apiLogBody {
	result := apiLogBody{truncated: truncated, incomplete: incomplete}
	switch {
	case truncated:
		result.omitted = fmt.Sprintf("body exceeds %d-byte capture limit", limit)
		return result
	case incomplete:
		result.omitted = "response body was not fully read"
		return result
	case len(strings.TrimSpace(string(data))) == 0:
		return result
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		result.omitted = "body is not valid JSON"
		return result
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		result.omitted = "body contains multiple JSON values"
		return result
	}
	result.value = redactJSON(value)
	return result
}

func redactJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if sensitiveName(key) {
				typed[key] = "[REDACTED]"
			} else {
				typed[key] = redactJSON(child)
			}
		}
	case []any:
		for i := range typed {
			typed[i] = redactJSON(typed[i])
		}
	}
	return value
}

func sanitizedHeaders(headers http.Header) map[string][]string {
	if len(headers) == 0 {
		return nil
	}
	result := make(map[string][]string, len(headers))
	for name, values := range headers {
		if sensitiveName(name) {
			result[name] = []string{"[REDACTED]"}
			continue
		}
		result[name] = append([]string(nil), values...)
	}
	return result
}

func sanitizedURL(value *url.URL) string {
	if value == nil {
		return ""
	}
	copy := *value
	copy.User = nil
	copy.Fragment = ""
	query := copy.Query()
	for name := range query {
		if sensitiveName(name) {
			query[name] = []string{"[REDACTED]"}
		}
	}
	copy.RawQuery = query.Encode()
	return copy.String()
}

func isAuthenticationEndpoint(request *http.Request) bool {
	if request == nil || request.URL == nil {
		return false
	}
	path := strings.ToLower(request.URL.Path)
	return strings.Contains(path, "/auth/") || strings.HasSuffix(path, "/auth") ||
		strings.HasSuffix(path, "/tokens") || strings.Contains(path, "/tokens/") ||
		strings.Contains(path, "oauth") || strings.Contains(path, "ec2tokens") || strings.Contains(path, "s3tokens")
}

func sensitiveName(name string) bool {
	lower := strings.ToLower(name)
	for _, part := range strings.FieldsFunc(lower, func(value rune) bool {
		return value == '-' || value == '_' || value == '.' || value == ' '
	}) {
		if part == "key" {
			return true
		}
	}
	normalized := lower
	normalized = strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(normalized)
	for _, marker := range []string{
		"authorization", "password", "passwd", "token", "secret", "credential",
		"cookie", "privatekey", "accesskey", "apikey", "signature",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

var inlineSecretPattern = regexp.MustCompile(`(?i)(authorization|password|passwd|token|secret|credential|api[-_]?key)(\s*[:=]\s*)([^&\s,;]+)`)

func sanitizedError(err error) string {
	if err == nil {
		return ""
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		value := urlErr.Op
		if parsed, parseErr := url.Parse(urlErr.URL); parseErr == nil {
			value += " " + sanitizedURL(parsed)
		}
		if urlErr.Err != nil {
			value += ": " + urlErr.Err.Error()
		}
		return inlineSecretPattern.ReplaceAllString(value, "$1$2[REDACTED]")
	}
	return inlineSecretPattern.ReplaceAllString(err.Error(), "$1$2[REDACTED]")
}

func outcomeName(outcome Outcome) string {
	switch outcome {
	case Success:
		return "success"
	case Timeout:
		return "timeout"
	default:
		return "error"
	}
}

func readLimited(reader io.Reader, limit int64) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}

type bodyCapture struct {
	data      []byte
	size      int64
	limit     int64
	truncated bool
}

func newBodyCapture(limit int64) *bodyCapture {
	return &bodyCapture{limit: limit}
}

func (c *bodyCapture) add(data []byte) {
	if c == nil || len(data) == 0 {
		return
	}
	c.size += int64(len(data))
	remaining := c.limit - int64(len(c.data))
	if remaining > 0 {
		count := int64(len(data))
		if count > remaining {
			count = remaining
		}
		c.data = append(c.data, data[:int(count)]...)
	}
	if c.size > c.limit {
		c.truncated = true
	}
}

var fallbackCallID atomic.Uint64

func newAPICallID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
	}
	return fmt.Sprintf("fallback-%016x", fallbackCallID.Add(1))
}
