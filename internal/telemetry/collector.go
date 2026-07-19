// Package telemetry instruments OpenStack HTTP requests. Its collector keeps
// anonymous timing and outcome statistics in memory; the separately enabled
// APILogger can persist sanitized debugging records.
package telemetry

import (
	"sort"
	"sync"
	"time"
)

const DefaultSlowThreshold = time.Second

type Outcome int

const (
	Success Outcome = iota
	Timeout
	Failure
)

type endpointData struct {
	calls     int
	successes int
	slow      int
	timeouts  int
	errors    int
	durations []time.Duration
	total     time.Duration
}

// Collector is safe for concurrent use by the shared OpenStack HTTP client.
type Collector struct {
	mu            sync.RWMutex
	slowThreshold time.Duration
	startedAt     time.Time
	generation    uint64
	endpoints     map[string]*endpointData
}

func NewCollector(slowThreshold time.Duration) *Collector {
	if slowThreshold <= 0 {
		slowThreshold = DefaultSlowThreshold
	}
	return &Collector{
		slowThreshold: slowThreshold,
		startedAt:     time.Now(),
		generation:    1,
		endpoints:     map[string]*endpointData{},
	}
}

// Begin captures the reset generation for one in-flight request. Finish drops
// observations from an older generation, so Reset remains a true zero point.
func (c *Collector) Begin() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.generation
}

func (c *Collector) Finish(generation uint64, endpoint string, duration time.Duration, outcome Outcome) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != c.generation {
		return
	}
	c.observeLocked(endpoint, duration, outcome)
}

// Observe records a completed logical observation in the current generation.
// It is useful for adapters and deterministic tests; HTTP instrumentation uses
// Begin and Finish so resets can discard older in-flight requests.
func (c *Collector) Observe(endpoint string, duration time.Duration, outcome Outcome) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.observeLocked(endpoint, duration, outcome)
}

func (c *Collector) observeLocked(endpoint string, duration time.Duration, outcome Outcome) {
	if endpoint == "" {
		endpoint = "UNKNOWN openstack /"
	}
	if duration < 0 {
		duration = 0
	}
	data := c.endpoints[endpoint]
	if data == nil {
		data = &endpointData{}
		c.endpoints[endpoint] = data
	}
	data.calls++
	switch outcome {
	case Success:
		data.successes++
	case Timeout:
		data.timeouts++
	default:
		data.errors++
	}
	if duration >= c.slowThreshold {
		data.slow++
	}
	data.durations = append(data.durations, duration)
	data.total += duration
}

func (c *Collector) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.startedAt = time.Now()
	c.endpoints = map[string]*endpointData{}
}

type Snapshot struct {
	StartedAt     time.Time
	CapturedAt    time.Time
	SlowThreshold time.Duration
	Calls         int
	Successes     int
	Slow          int
	Timeouts      int
	Errors        int
	Endpoints     []EndpointStats
}

type EndpointStats struct {
	Endpoint  string
	Calls     int
	Successes int
	Slow      int
	Timeouts  int
	Errors    int
	Min       time.Duration
	Max       time.Duration
	Average   time.Duration
	Median    time.Duration
	P95       time.Duration
	P99       time.Duration
}

func (c *Collector) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	snapshot := Snapshot{
		StartedAt: c.startedAt, CapturedAt: time.Now(), SlowThreshold: c.slowThreshold,
		Endpoints: make([]EndpointStats, 0, len(c.endpoints)),
	}
	for endpoint, data := range c.endpoints {
		durations := append([]time.Duration(nil), data.durations...)
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		stats := EndpointStats{
			Endpoint: endpoint, Calls: data.calls, Successes: data.successes,
			Slow: data.slow, Timeouts: data.timeouts, Errors: data.errors,
		}
		if len(durations) > 0 {
			stats.Min = durations[0]
			stats.Max = durations[len(durations)-1]
			stats.Average = data.total / time.Duration(len(durations))
			stats.Median = median(durations)
			stats.P95 = percentile(durations, 0.95)
			stats.P99 = percentile(durations, 0.99)
		}
		snapshot.Calls += stats.Calls
		snapshot.Successes += stats.Successes
		snapshot.Slow += stats.Slow
		snapshot.Timeouts += stats.Timeouts
		snapshot.Errors += stats.Errors
		snapshot.Endpoints = append(snapshot.Endpoints, stats)
	}
	sort.Slice(snapshot.Endpoints, func(i, j int) bool {
		if snapshot.Endpoints[i].Calls != snapshot.Endpoints[j].Calls {
			return snapshot.Endpoints[i].Calls > snapshot.Endpoints[j].Calls
		}
		return snapshot.Endpoints[i].Endpoint < snapshot.Endpoints[j].Endpoint
	})
	return snapshot
}

func median(sorted []time.Duration) time.Duration {
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return sorted[mid-1] + (sorted[mid]-sorted[mid-1])/2
}

func percentile(sorted []time.Duration, quantile float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(float64(len(sorted))*quantile+0.999999999) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}
