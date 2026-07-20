package telemetry

import (
	"runtime"
	"runtime/metrics"
	"sync"
	"time"
)

var applicationStartedAt = time.Now()

var applicationHighWater struct {
	sync.Mutex
	goroutines  int
	threads     int
	heapAlloc   uint64
	heapInuse   uint64
	heapObjects uint64
	stackInuse  uint64
	runtimeSys  uint64
}

// ApplicationSnapshot captures portable Go runtime health metrics. RuntimeSys
// is memory reserved from the OS by the Go runtime; it is not process RSS.
type ApplicationSnapshot struct {
	StartedAt     time.Time
	CapturedAt    time.Time
	Uptime        time.Duration
	Goroutines    int
	MaxGoroutines int
	Threads       int
	MaxThreads    int
	GOMAXPROCS    int
	LogicalCPUs   int

	HeapAlloc      uint64
	MaxHeapAlloc   uint64
	HeapInuse      uint64
	MaxHeapInuse   uint64
	HeapObjects    uint64
	MaxHeapObjects uint64
	StackInuse     uint64
	MaxStackInuse  uint64
	RuntimeSys     uint64
	MaxRuntimeSys  uint64
}

func CaptureApplicationSnapshot() ApplicationSnapshot {
	now := time.Now()
	uptime := now.Sub(applicationStartedAt)
	if uptime < 0 {
		uptime = 0
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	goroutines := runtime.NumGoroutine()
	threads := runtimeThreadCount()
	applicationHighWater.Lock()
	defer applicationHighWater.Unlock()
	applicationHighWater.goroutines = max(applicationHighWater.goroutines, goroutines)
	applicationHighWater.threads = max(applicationHighWater.threads, threads)
	applicationHighWater.heapAlloc = max(applicationHighWater.heapAlloc, memory.HeapAlloc)
	applicationHighWater.heapInuse = max(applicationHighWater.heapInuse, memory.HeapInuse)
	applicationHighWater.heapObjects = max(applicationHighWater.heapObjects, memory.HeapObjects)
	applicationHighWater.stackInuse = max(applicationHighWater.stackInuse, memory.StackInuse)
	applicationHighWater.runtimeSys = max(applicationHighWater.runtimeSys, memory.Sys)
	return ApplicationSnapshot{
		StartedAt: applicationStartedAt, CapturedAt: now, Uptime: uptime,
		Goroutines: goroutines, MaxGoroutines: applicationHighWater.goroutines,
		Threads: threads, MaxThreads: applicationHighWater.threads,
		GOMAXPROCS: runtime.GOMAXPROCS(0), LogicalCPUs: runtime.NumCPU(),
		HeapAlloc: memory.HeapAlloc, MaxHeapAlloc: applicationHighWater.heapAlloc,
		HeapInuse: memory.HeapInuse, MaxHeapInuse: applicationHighWater.heapInuse,
		HeapObjects: memory.HeapObjects, MaxHeapObjects: applicationHighWater.heapObjects,
		StackInuse: memory.StackInuse, MaxStackInuse: applicationHighWater.stackInuse,
		RuntimeSys: memory.Sys, MaxRuntimeSys: applicationHighWater.runtimeSys,
	}
}

func runtimeThreadCount() int {
	samples := []metrics.Sample{{Name: "/sched/threads/total:threads"}}
	metrics.Read(samples)
	if samples[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return int(samples[0].Value.Uint64())
}
