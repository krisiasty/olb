package telemetry

import "testing"

func TestCaptureApplicationSnapshot(t *testing.T) {
	snapshot := CaptureApplicationSnapshot()
	if snapshot.StartedAt.IsZero() || snapshot.CapturedAt.IsZero() || snapshot.Uptime < 0 {
		t.Fatalf("invalid application timing: %+v", snapshot)
	}
	if snapshot.Goroutines < 1 || snapshot.Threads < 1 || snapshot.GOMAXPROCS < 1 || snapshot.LogicalCPUs < 1 {
		t.Fatalf("invalid runtime concurrency metrics: %+v", snapshot)
	}
	if snapshot.RuntimeSys == 0 || snapshot.HeapInuse < snapshot.HeapAlloc {
		t.Fatalf("invalid runtime memory metrics: %+v", snapshot)
	}
	if snapshot.MaxGoroutines < snapshot.Goroutines || snapshot.MaxThreads < snapshot.Threads ||
		snapshot.MaxHeapAlloc < snapshot.HeapAlloc || snapshot.MaxHeapInuse < snapshot.HeapInuse ||
		snapshot.MaxHeapObjects < snapshot.HeapObjects || snapshot.MaxStackInuse < snapshot.StackInuse ||
		snapshot.MaxRuntimeSys < snapshot.RuntimeSys {
		t.Fatalf("invalid application high-water marks: %+v", snapshot)
	}
}
