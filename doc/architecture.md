# Architecture

This document describes the high-level architecture of the Whereabouts IPAM
CNI plugin.

## Binaries

Whereabouts ships three binaries:

| Binary | Role |
|--------|------|
| `whereabouts` | CNI plugin binary, called by the container runtime (via Multus) on pod create/delete |
| `whereabouts-operator` | Operator binary — `controller` subcommand runs reconcilers + webhook server |
| `install-cni` | DaemonSet entry-point — copies CNI binary to host, generates kubeconfig/conf, watches token rotation |

## CNI Plugin (`cmd/whereabouts/main.go`)

Implements the standard CNI interface:

* **ADD** — allocates the lowest available IP from the configured range(s),
  persists the reservation in an IPPool CRD, and returns it to the runtime.
  Idempotent: re-running ADD for the same pod+interface returns the existing
  allocation.
* **DEL** — releases the IP reservation for the given container ID + interface.
* **CHECK** — verifies that the previously allocated IP is still present on the
  interface (CNI spec compliance).

## Operator (`cmd/operator/`)

Built on [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime)
with a single `controller` subcommand that runs both reconcilers and the
webhook server from the same process:

* Reconcilers are leader-elected — only one replica runs them.
* All replicas serve validating webhooks with automatic TLS rotation
  (via `cert-controller`).

### Reconcilers (`internal/controller/`)

| Reconciler | Watches | Purpose |
|-----------|---------|---------|
| `IPPoolReconciler` | IPPool CRDs | Removes orphaned allocations by checking podRef against live pods |
| `NodeSliceReconciler` | NetworkAttachmentDefinitions + Nodes | Manages NodeSlicePool CRDs for Fast IPAM |
| `OverlappingRangeReconciler` | OverlappingRangeIPReservation CRDs | Deletes orphaned reservations |

Reconciler behavior can be tuned with feature flags:

| Flag | Default | Effect |
|------|---------|--------|
| `--cleanup-terminating-pods` | `false` | Release IPs from pods that have `DeletionTimestamp` set |
| `--cleanup-disrupted-pods` | `true` | Release IPs from pods with `DisruptionTarget` condition |
| `--verify-network-status` | `true` | Verify allocations against Multus `network-status` annotation |
| `--reconcile-interval` | `30s` | Interval between reconciliation cycles |

### Webhooks (`internal/webhook/`)

Typed `admission.Validator[T]` implementations:

| Validator | Validates |
|-----------|-----------|
| `IPPoolValidator` | Range CIDR format, podRef `"namespace/name"` format |
| `NodeSlicePoolValidator` | Range CIDR, SliceSize integer 1–128 |
| `OverlappingRangeValidator` | podRef `"namespace/name"` format |

Webhook manifests include `matchConditions` CEL expressions to bypass
validation for the CNI plugin's own ServiceAccount.

### TLS Certificate Rotation (`internal/webhook/certrotator/`)

The operator wraps [cert-controller](https://github.com/open-policy-agent/cert-controller)
to automatically manage webhook TLS certificates. Certificates are:
- Created in a Kubernetes Secret (`--webhook-secret-name`)
- Rotated before expiry without manual intervention
- CA bundle injected into the ValidatingWebhookConfiguration

## IPAM Features

The CNI plugin supports several allocation modes and features:

| Feature | Config Parameter | Description |
|---------|-----------------|-------------|
| L3/Routed mode | `enable_l3` | Allocate all IPs including `.0` and broadcast addresses |
| Gateway exclusion | `exclude_gateway` | Auto-exclude the gateway IP from allocation |
| Optimistic IPAM | `optimistic_ipam` | Bypass leader election for faster allocation at scale |
| Preferred/Sticky IP | Pod annotation `whereabouts.cni.cncf.io/preferred-ip` | Assign a specific IP if available |
| Small subnets | N/A | /31, /32, /127, /128 subnets supported out of the box |
| Dual-stack | `ipRanges` | Multi-range allocation for IPv4 + IPv6 |
| Named networks | `network_name` | Isolate IPPool CRs per logical network |
| Fast IPAM | `node_slice_size` | Per-node IP slices to reduce contention at scale |

See [extended-configuration.md](extended-configuration.md) for full details.

## IP Allocation Flow

```
Pod Create → kubelet → Multus → whereabouts CNI ADD
                                      │
                                      ├─ Parse IPAM config (inline + flat-file merge)
                                      ├─ Acquire leader election lease
                                      ├─ Get/create IPPool CRD for range
                                      ├─ Check for existing allocation (idempotent)
                                      ├─ Find lowest available IP (IterateForAssignment)
                                      ├─ Update IPPool CRD (with retries, up to 100)
                                      ├─ Create OverlappingRangeIPReservation (if enabled)
                                      └─ Return IP to runtime
```

## Storage Layer (`pkg/storage/`)

* `Store` and `IPPool` interfaces defined in `storage.go`.
* Sole production implementation: `pkg/storage/kubernetes/` using IPPool CRDs
  with JSON Patch + optimistic locking (resource version checks with retry).

### Custom Resource Definitions

| CRD | Purpose |
|-----|---------|
| `IPPool` | Stores IP allocations per range. Key format: `<namespace>-<network-name>-<normalized-range>` |
| `OverlappingRangeIPReservation` | Ensures IP uniqueness across overlapping ranges (enabled by default) |
| `NodeSlicePool` | Tracks per-node IP slice allocations for Fast IPAM (experimental) |

## Key Packages

| Package | Responsibility |
|---------|---------------|
| `pkg/allocate` | IP assignment algorithm — finds lowest available IP |
| `pkg/config` | Merges inline IPAM JSON → flat file → defaults |
| `pkg/iphelpers` | IP arithmetic: offsets, ranges, CIDR splitting |
| `pkg/storage/kubernetes` | CRD-based storage with retry logic |
| `pkg/types` | Core data structures (IPAMConfig, IPReservation) |
| `pkg/logging` | Structured logging with configurable levels |

## Configuration Hierarchy

Whereabouts merges configuration from multiple sources (high → low priority):

1. Inline IPAM config in the NetworkAttachmentDefinition
2. CNI config file parameters
3. Flat file at `configuration_path` or default locations

See [extended-configuration.md](extended-configuration.md) for the full
parameter reference.

## Related Documentation

- [Extended Configuration](extended-configuration.md) — Full IPAM parameter reference, operator flags, webhook config, debugging
- [Metrics](metrics.md) — Prometheus metrics, alerts, Grafana dashboard
- [Developer Notes](developer_notes.md) — Build commands, testing, e2e setup, debugging workflows
