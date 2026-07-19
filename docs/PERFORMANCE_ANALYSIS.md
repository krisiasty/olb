# Reference Guide: Diagnosing & Optimizing OpenStack Octavia API Latency

This reference document outlines the technical architecture, performance
baselines, and step-by-step troubleshooting procedures for addressing high
latency on the OpenStack Octavia `/v2.0/lbaas/loadbalancers/:id/stats` endpoint.

---

## 1. Architectural Context & Performance Baselines

### Normal vs. Degraded Performance Profiles

Under healthy operating conditions, telemetry operations should run within tight
windows. On a quiet or private cluster, any variation beyond these targets
points to structural or database inefficiencies.

| Metric | Target Baseline | Your Environment | Status | Diagnosis |
| --- | --- | --- | --- | --- |
| Minimum Latency | < 50ms | 184ms | High Baseline | Fixed overhead: API init, SQLAlchemy setup, token parsing. |
| Average Latency | 100ms – 300ms | 680ms | Slow | Bottleneck during data aggregation and object serialization. |
| Median Latency | 100ms – 300ms | 780ms | Slow | Heavy skew to the slow end: degradation is systemic, not transient. |
| p95 / p99 Latency | < 500ms | 883ms / 1.2s | Critical | Thread starvation or DB lock contention under concurrency. |

### Endpoint Comparison Analysis

Comparing your telemetry data across endpoints isolates the root cause:

- `GET /v2.0/floatingips` completes in 99ms (Avg), proving network routing and
  Memcached token validation are optimal.
- `GET /v2.0/lbaas/loadbalancers/:id` completes in 359ms (Avg), defining the raw
  cost of building parent load balancer data structures.
- `GET /v2.0/lbaas/loadbalancers/:id/stats` jumps to 680ms (Avg).

Because both Octavia endpoints share identical network and authorization steps,
the extra ~320ms is spent exclusively within the Python control plane parsing
database entries and calculating statistical aggregates.

---

## 2. Root Cause Analysis

### A. Python-SQLAlchemy Object Inflation Overhead

OpenStack utilizes SQLAlchemy as an Object-Relational Mapper (ORM). When
querying the `/stats` endpoint, the `octavia-api` daemon fetches matching records
from the database and instantiates them as complex Python objects. Python's
single-threaded nature per API worker means that translating multiple nested
database entries (Load Balancer → Listeners → Statistics Rows) into JSON is
highly CPU-bound and creates an immediate serialization bottleneck. [1]

### B. Database Table Fragmentation & Bloat

The `octavia-health-manager` daemon continuously writes heartbeat and performance
telemetry into the database at high frequencies. Over time, tables like
`listener_statistics` and `amphora_health` become heavily fragmented. Even on an
idle cluster with no active network traffic, the database engine must scan
through historical data files and fragmented indexes to aggregate rows for your
listeners, causing read queries to stall.

### C. Missing or Corrupted Housekeeping Automations

If the `octavia-housekeeping` daemon is unconfigured, dead, or failing to execute
its cleanup cycles, historical telemetry data from deleted amphorae and old
listeners is never purged. This forces synchronous API calls to sort through
millions of orphaned rows to calculate data for an active load balancer.

---

## 3. Database Diagnostic & Optimization Procedures

When administrative access is available to the MariaDB/MySQL cluster, execute the
following commands to assess and repair the database tier.

### Step 1: Check Total Telemetry Bloat

Determine if the database is retaining an excessive amount of historical tracking
records:

```sql
SELECT COUNT(*) AS total_rows, COUNT(DISTINCT listener_id) AS active_listeners
FROM octavia.listener_statistics;
```

- **Analysis:** If `total_rows` numbers in the hundreds of thousands or millions
  while `active_listeners` is low, the housekeeping cleanup cycles are failing.

### Step 2: Isolate Raw SQL Execution Speed

Profile the exact execution time of the query used by Octavia to calculate
statistics, bypassing the Python API layer completely:

```sql
SET profiling = 1;
SELECT * FROM octavia.listener_statistics WHERE listener_id IN (
    SELECT id FROM octavia.listener
    WHERE load_balancer_id = 'YOUR_LOAD_BALANCER_UUID_HERE'
);
SHOW PROFILE;
```

- **Result < 10ms:** The database engine is healthy. The bottleneck is entirely
  inside the `octavia-api` Python serialization logic.
- **Result > 50ms:** The database tables are fragmented, unindexed, or choked by
  locking contention. Proceed to Step 3.

### Step 3: Defragment and Optimize Target Tables

Rebuild indexes and reclaim empty storage space to speed up lookups:

```sql
ANALYZE TABLE octavia.listener_statistics;
OPTIMIZE TABLE octavia.listener_statistics;
OPTIMIZE TABLE octavia.load_balancer_statistics;
OPTIMIZE TABLE octavia.amphora_health;
```

---

## 4. Control Plane Configuration Adjustments

Review and modify `/etc/octavia/octavia.conf` on your controller nodes to
optimize the environment.

### A. Enable Database Connection Pooling

Ensure the API service doesn't waste time opening and closing database handles
for every single metrics request:

```ini
[database]
max_pool_size = 50
max_overflow = 30
pool_timeout = 30
```

### B. Scale API Worker Threads

Increase concurrency to prevent CPU-heavy SQLAlchemy object serialization from
queueing up incoming API requests:

```ini
[api_settings]
# Match the number of physical CPU cores on your controller node
api_workers = 8
```

### C. Verify and Configure Housekeeping Intervals

Ensure the background deletion processes are active and aggressive enough to
prevent data accumulation:

```ini
[house_keeping]
# Interval in seconds to execute the cleanup cycle
cleanup_interval = 3600
# Duration in seconds to retain historical amphora records before deletion
amphora_expiry_age = 604800
```

After making configuration changes, restart the corresponding system daemons:

```bash
systemctl restart openstack-octavia-api.service
systemctl restart openstack-octavia-housekeeping.service
```

---

## 5. Telemetry Architecture Recommendations

If configuration tuning does not bring the endpoint under 300ms, the architecture
of the telemetry collection tool should be adapted. Polling root load balancer
object trees via REST APIs at scale introduces a perpetual tax on the OpenStack
control plane.

### Option A: Query Listener Endpoints Directly

Your metrics show that `GET /v2.0/lbaas/listeners` responds in 124ms (Avg). You
can completely bypass the slow parent load balancer tree calculation by having
your monitoring agent fetch metrics for specific listeners directly:

```text
GET /v2.0/lbaas/listeners/:listener_id/stats
```

Because this maps to a direct, single-row indexed lookup inside the database, it
eliminates complex multi-table SQL joins and object mapping loops.

### Option B: Transition to Native Prometheus Drivers

For production-grade monitoring, decouple telemetry completely from the OpenStack
API controllers. Configure Octavia to expose metrics directly from the Amphora
instances using the native Prometheus driver. This allows Prometheus to scrape
the load balancers over the network on a dedicated port, bypassing Keystone,
Octavia-API, and MariaDB entirely.

---

## Next Steps Checklist

- Run the SQL profiling queries in Section 3 to isolate the DB execution time
  from Python processing.
- Check the status of `openstack-octavia-housekeeping` on the controllers.
- Evaluate if your telemetry collector can be updated to poll individual listener
  IDs instead of the root load balancer stats.

Let me know if you need to review the specific SQL syntax for automated retention
scripts or want to look at the Prometheus exporter configurations once you gain
admin access.

## References

1. [Implementation of Clean Architecture in Python](https://www.linkedin.com/pulse/implementation-clean-architecture-python-part-4-adding-watanabe)
