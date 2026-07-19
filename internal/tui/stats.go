package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

var rateCounterKeys = [...]string{
	"total_connections",
	"request_errors",
	"bytes_in",
	"bytes_out",
}

type statChange struct {
	delta float64
	rate  float64
}

// applyStatsSample commits a successful stats response and derives rates from
// the previous successful sample. A counter decrease indicates an Octavia
// reset, so that counter has no change until the following sample.
func (m *Model) applyStatsSample(lbID string, stats map[string]any, sampledAt time.Time) {
	if sampledAt.IsZero() {
		sampledAt = m.clock()
	}
	changes := map[string]statChange{}
	previous, hasPrevious := m.lbStats[lbID]
	previousAt := m.lbStatsSampledAt[lbID]
	if elapsed := sampledAt.Sub(previousAt).Seconds(); hasPrevious && !previousAt.IsZero() && elapsed > 0 {
		for _, key := range rateCounterKeys {
			currentValue, currentOK := statNumber(stats[key])
			previousValue, previousOK := statNumber(previous[key])
			if currentOK && previousOK && currentValue >= previousValue {
				delta := currentValue - previousValue
				changes[key] = statChange{delta: delta, rate: delta / elapsed}
			}
		}
	}
	m.lbStats[lbID] = stats
	m.lbStatsChanges[lbID] = changes
	m.lbStatsSampledAt[lbID] = sampledAt
}

func statNumber(value any) (float64, bool) {
	if value == nil {
		return 0, false
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(value)), 64)
	return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0
}

func formatStatCount(value any) string {
	raw := strings.TrimSpace(fmt.Sprint(value))
	if raw == "" {
		return "—"
	}
	sign := ""
	digits := raw
	if strings.HasPrefix(digits, "-") || strings.HasPrefix(digits, "+") {
		sign, digits = digits[:1], digits[1:]
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return raw
		}
	}
	if len(digits) <= 3 {
		return raw
	}
	first := len(digits) % 3
	if first == 0 {
		first = 3
	}
	var b strings.Builder
	b.WriteString(sign)
	b.WriteString(digits[:first])
	for i := first; i < len(digits); i += 3 {
		b.WriteByte(',')
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

func formatStatBytes(value any) string {
	n, ok := statNumber(value)
	if !ok {
		return fmt.Sprint(value)
	}
	return formatIEC(n)
}

func formatIEC(value float64) string {
	units := [...]string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	return formatDecimal(value) + " " + units[unit]
}

func formatCounterRate(value float64) string {
	return formatDecimal(value) + "/s"
}

func formatCounterDelta(value float64) string {
	return formatStatCount(strconv.FormatFloat(value, 'f', -1, 64))
}

func formatByteRate(value float64) string {
	return formatIEC(value) + "/s"
}

func formatDecimal(value float64) string {
	precision := 2
	switch {
	case value >= 100:
		precision = 0
	case value >= 10:
		precision = 1
	case value > 0 && value < 0.1:
		precision = 3
	}
	formatted := strconv.FormatFloat(value, 'f', precision, 64)
	if strings.Contains(formatted, ".") {
		formatted = strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
	}
	return formatted
}
