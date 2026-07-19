package osclient

import "github.com/krisiasty/olb/internal/telemetry"

// TelemetrySnapshot returns a concurrency-safe copy of the in-memory API
// metrics collected since authentication or the last reset.
func (c *Clients) TelemetrySnapshot() telemetry.Snapshot {
	if c == nil || c.telemetry == nil {
		return telemetry.Snapshot{}
	}
	return c.telemetry.Snapshot()
}

func (c *Clients) ResetTelemetry() {
	if c != nil && c.telemetry != nil {
		c.telemetry.Reset()
	}
}
