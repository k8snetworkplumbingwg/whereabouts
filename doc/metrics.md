# Operator Metrics

The whereabouts operator exposes Prometheus metrics via the controller-runtime
metrics endpoint. All metrics (controller and webhook) are served from the same
process on the standard `/metrics` path:

| Default Address | Override Flag | Description |
|-----------------|---------------|-------------|
| `:8080` | `--metrics-bind-address` | Prometheus metrics endpoint for both reconciler and webhook metrics |

> **Note**: The operator uses a single `controller` subcommand that runs both
> reconcilers and the webhook server in the same process. All metrics are served
> from one endpoint.

## Built-in Metrics

controller-runtime provides these out of the box:

| Metric | Type | Description |
|--------|------|-------------|
| `controller_runtime_reconcile_total` | Counter | Total reconciliations by controller and result |
| `controller_runtime_reconcile_errors_total` | Counter | Total reconciliation errors by controller |
| `controller_runtime_reconcile_time_seconds` | Histogram | Reconciliation duration by controller |
| `controller_runtime_webhook_requests_total` | Counter | Webhook requests by webhook path and HTTP code |
| `controller_runtime_webhook_latency_seconds` | Histogram | Webhook request latency by path |
| `workqueue_depth` | Gauge | Work queue depth by queue name |
| `workqueue_adds_total` | Counter | Items added to work queues |

## Custom Metrics

### Controller Metrics

Registered on the `controller` subcommand's metrics endpoint.

#### `whereabouts_ippool_allocations`

- **Type:** Gauge
- **Labels:** `pool` (IPPool name)
- **Description:** Current number of IP allocations in each IPPool. Updated on
  every reconciliation. Useful for capacity monitoring and alerting when pools
  approach exhaustion.

#### `whereabouts_ippool_orphans_cleaned_total`

- **Type:** Counter
- **Labels:** `pool` (IPPool name)
- **Description:** Total number of orphaned allocations removed from IP pools.
  An orphaned allocation is one whose referenced pod no longer exists, has been
  marked for deletion by the taint manager, or whose IP is not present in the
  pod's Multus network-status annotation.

#### `whereabouts_overlappingrange_reservations_cleaned_total`

- **Type:** Counter
- **Labels:** none
- **Description:** Total number of orphaned OverlappingRangeIPReservation CRDs
  deleted. Incremented by both the IPPoolReconciler (inline cleanup during
  orphan removal) and the OverlappingRangeReconciler (dedicated reservation
  watcher).

#### `whereabouts_nodeslicepool_assigned_nodes`

- **Type:** Gauge
- **Labels:** `pool` (NodeSlicePool name)
- **Description:** Number of nodes with assigned IP slices in each
  NodeSlicePool. Useful for detecting when a pool is full (assigned equals
  total slices) and new nodes cannot get IP ranges.

#### `whereabouts_nodeslicepool_slices_total`

- **Type:** Gauge
- **Labels:** `pool` (NodeSlicePool name)
- **Description:** Total number of IP slices in each NodeSlicePool. Determined
  by dividing the CIDR range by the configured `node_slice_size`.

### Webhook Metrics

Registered on the controller manager's metrics endpoint.

#### `whereabouts_webhook_validation_total`

- **Type:** Counter
- **Labels:** `resource` (`ippool` | `nodeslicepool` | `overlappingrange`),
  `operation` (`create` | `update` | `delete`),
  `result` (`allowed` | `rejected`)
- **Description:** Total webhook validation decisions. Tracks how many
  admission requests were allowed or rejected, broken down by resource type and
  operation. Complements controller-runtime's generic webhook metrics with
  domain-specific outcome tracking.

## Example Prometheus Alerts

```yaml
groups:
  - name: whereabouts
    rules:
      # Alert when an IP pool is more than 90% full.
      # TODO: This alert requires a whereabouts_ippool_capacity metric that
      # is not yet implemented. Uncomment once the metric is available.
      # - alert: IPPoolNearlyFull
      #   expr: |
      #     whereabouts_ippool_allocations / on(pool) group_left
      #     whereabouts_ippool_capacity > 0.9
      #   for: 5m
      #   labels:
      #     severity: warning
      #   annotations:
      #     summary: "IPPool {{ $labels.pool }} is {{ $value | humanizePercentage }} full"

      # Alert on sustained orphan cleanup — may indicate pod churn issues.
      - alert: HighOrphanCleanupRate
        expr: rate(whereabouts_ippool_orphans_cleaned_total[5m]) > 1
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "High orphan cleanup rate in pool {{ $labels.pool }}"

      # Alert when webhook rejection rate is elevated.
      - alert: WebhookRejectionRate
        expr: |
          rate(whereabouts_webhook_validation_total{result="rejected"}[5m])
          / rate(whereabouts_webhook_validation_total[5m]) > 0.1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Elevated webhook rejection rate for {{ $labels.resource }}"
```

## Grafana Dashboard

A basic dashboard can be built with these panels:

1. **IP Pool Utilization** — `whereabouts_ippool_allocations` per pool
2. **Orphan Cleanup Rate** — `rate(whereabouts_ippool_orphans_cleaned_total[5m])`
3. **Reconciliation Latency** — `controller_runtime_reconcile_time_seconds` histogram
4. **Webhook Validation** — `whereabouts_webhook_validation_total` stacked by result
5. **Node Slice Assignment** — `whereabouts_nodeslicepool_assigned_nodes` vs `whereabouts_nodeslicepool_slices_total`

## Prometheus Scraping Setup

To scrape metrics from the operator, create a `ServiceMonitor` (requires the
[Prometheus Operator](https://github.com/prometheus-operator/prometheus-operator)):

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: whereabouts-metrics
  namespace: kube-system
  labels:
    app: whereabouts-controller
spec:
  selector:
    matchLabels:
      app: whereabouts-controller
  endpoints:
  - port: metrics
    interval: 30s
    path: /metrics
```

Ensure the operator's metrics Service exposes port `8080` (the default
`--metrics-bind-address`). The kustomize-based deployment (`make deploy`)
includes the necessary Service definition.

For non-Prometheus-Operator setups, add a scrape config:

```yaml
scrape_configs:
  - job_name: whereabouts
    kubernetes_sd_configs:
      - role: endpoints
        namespaces:
          names: [kube-system]
    relabel_configs:
      - source_labels: [__meta_kubernetes_service_label_app]
        regex: whereabouts-controller
        action: keep
```
