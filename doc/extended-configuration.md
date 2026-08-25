# Extended configuration

Should you need to further configure Whereabouts, you might find these options valuable.

## IP Reconciliation

Whereabouts includes an IP reconciliation mechanism that continuously scans
allocated IP addresses, reconciles them against currently running pods, and
deallocates IP addresses which have been left stranded.

Stranded IP addresses can occur due to node failures (e.g. a sudden power off /
reboot event) or potentially from pods that have been force deleted
(e.g. `kubectl delete pod foo --grace-period=0 --force`)

The reconciler runs as part of the **whereabouts-operator** (deployed via
`make deploy`). The reconciliation interval is configured
via the `--reconcile-interval` flag on the operator's `controller` subcommand
(default: `30s`).

## Installation options

The daemonset installation as shown on the README is for use with Kubernetes version 1.16 and later. It may also be useful with previous versions, however you'll need to change the `apiVersion` of the daemonset in the provided yaml, [see the deprecation notice](https://kubernetes.io/blog/2019/07/18/api-deprecations-in-1-16/).

You can compile from this repo (with `make build`) to produce a CNI binary, or deploy the operator image via `make deploy`.

Note that we're also including a Custom Resource Definition (CRD) to use the `kubernetes` datastore option. This installs the kubernetes CRD specification for the `ippools.whereabouts.cni.cncf.io/v1alpha1` type.

### Logging Parameters

There are two optional parameters for logging, they are:

* `log_file`: A file path to a logfile to log to.
* `log_level`: Set the logging verbosity, from most to least: `debug`,`verbose`,`error`,`panic`

## Flatfile configuration

During installation using the daemonset-style install, Whereabouts creates a configuration file @ `/etc/cni/net.d/whereabouts.d/whereabouts.conf`. Any parameter that you do not wish to repeatedly put into the `ipam` section of a CNI configuration can be put into this file (such as Kubernetes configuration parameters or logging).

There is one option for flat file configuration:

* `configuration_path`: A file path to a Whereabouts configuration file.

If you're using [Multus CNI](http://multus-cni.io/) or another meta-plugin, you may wish to reduce the number of parameters you need to specify in the IPAM section by putting commonly used options into a flat file -- primarily to make it simpler to type and to reduce having to copy and paste the same parameters repeatedly.

Whereabouts will look for the configuration in these locations, in this order:

* The location specified by the `configuration_path` option.
* `/etc/kubernetes/cni/net.d/whereabouts.d/whereabouts.conf`
* `/etc/cni/net.d/whereabouts.d/whereabouts.conf`

You may specify the `configuration_path` to point to another location should it be desired.

Any options added to the `whereabouts.conf` are overridden by configuration options that are in the primary CNI configuration (e.g. in a custom resource `NetworkAttachmentDefinition` used by Multus CNI or in the first file ASCII-betically in the CNI configuration directory -- which is `/etc/cni/net.d/` by default).


### Example flat file configuration

You can reduce the number of parameters used if you need to make more than one Whereabouts configuration (such as if you're using [Multus CNI](http://multus-cni.io/))

Create a file named `/etc/cni/net.d/whereabouts.d/whereabouts.conf`, with the contents:

```
{
  "kubernetes": {
    "kubeconfig": "/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"
  },
  "log_file": "/tmp/whereabouts.log",
  "log_level": "debug"
}
```

With that in place, you can now create an IPAM configuration that has a lot less options, in this case we'll give an example using a `NetworkAttachmentDefinition` as used with Multus CNI (or other implementations of the [Network Plumbing Working Group specification](https://github.com/k8snetworkplumbingwg/multi-net-spec))

An example configuration using a `NetworkAttachmentDefinition`:

```
apiVersion: "k8s.cni.cncf.io/v1"
kind: NetworkAttachmentDefinition
metadata:
  name: whereabouts-conf
spec:
  config: '{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "192.168.2.225/28"
      }
    }'
```

You'll note that in the `ipam` section there's a lot less parameters than are used in the previous examples.

## Reconciler Interval Configuration (optional)

The IP reconciler runs as part of the whereabouts-operator. The reconciliation
interval can be configured via the `--reconcile-interval` flag on the operator's
`controller` subcommand. The default interval is `30s`.

To change it, edit the operator Deployment's command args:
```yaml
        command:
        - /whereabouts-operator
        - controller
        - --reconcile-interval=60s
```

## IPAM Configuration Reference

Below is a complete reference of all IPAM configuration parameters. All parameters
are specified inside the `"ipam"` object in CNI configuration JSON.

### Core Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `type` | string | yes | Must be `"whereabouts"` |
| `range` | string | yes* | CIDR notation for the IP range (e.g., `"192.168.2.0/24"`, `"2001:db8::/64"`) |
| `range_start` | string | no | First IP to allocate within the range |
| `range_end` | string | no | Last IP to allocate within the range |
| `exclude` | string[] | no | CIDRs to exclude from allocation |
| `gateway` | string | no | Gateway IP address for the interface |
| `exclude_gateway` | bool | no | When `true`, automatically excludes the gateway IP from allocation. Useful for L2 networks. See [Gateway IP Exclusion](#gateway-ip-exclusion-exclude_gateway). |
| `optimistic_ipam` | bool | no | Bypass leader election and rely on Kubernetes optimistic concurrency. See [Optimistic IPAM](#optimistic-ipam-optimistic_ipam). |
| `enable_l3` | bool | no | Enable L3/routed mode where all IPs in the subnet are allocatable (no network/broadcast exclusion). See [L3/Routed Mode](#l3routed-mode-enable_l3). |

*\*Required unless using `ipRanges`.*

### Multi-Range / Dual-Stack

| Parameter | Type | Description |
|-----------|------|-------------|
| `ipRanges` | object[] | Array of range objects for multi-IP or dual-stack allocation. Each element supports `range`, `range_start`, `range_end`, and `exclude`. |

Example dual-stack configuration:
```json
{
  "type": "whereabouts",
  "ipRanges": [
    {"range": "192.168.2.0/24"},
    {"range": "2001:db8::/64", "range_start": "2001:db8::10", "range_end": "2001:db8::ff"}
  ]
}
```

### Network Isolation

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `network_name` | string | `""` | Logical network name. Creates separate IPPool CRs per network, allowing the same CIDR range to be used independently in multi-tenant scenarios. |
| `enable_overlapping_ranges` | bool | `true` | Enables cluster-wide IP uniqueness checks via OverlappingRangeIPReservation CRDs. Prevents the same IP from being allocated across different ranges. |

### Fast IPAM (Experimental)

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `node_slice_size` | string | `""` | Prefix length for per-node IP slices (e.g., `"28"` or `"/28"`). Enables the experimental Fast IPAM feature, which pre-allocates IP slices per node to reduce allocation contention in large clusters. Requires the operator's NodeSliceReconciler (deployed via `make deploy`). Valid range: 1–128. |

### Leader Election

These parameters configure the leader election used during IP allocation. All values
are in **milliseconds**. Defaults are suitable for most deployments.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `leader_lease_duration` | int | `1500` | Leader election lease duration (ms) |
| `leader_renew_deadline` | int | `1000` | Leader election renew deadline (ms) |
| `leader_retry_period` | int | `500` | Leader election retry period (ms) |

### Logging

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `log_file` | string | `""` | Path to the whereabouts log file. If empty, logs go to stderr. |
| `log_level` | string | `""` | Logging verbosity: `"debug"`, `"verbose"`, `"error"`, or `"panic"`. |

### Kubernetes Configuration

| Parameter | Type | Description |
|-----------|------|-------------|
| `kubernetes.kubeconfig` | string | Path to a kubeconfig file. If empty, in-cluster configuration is used. |

### Other

| Parameter | Type | Description |
|-----------|------|-------------|
| `configuration_path` | string | Path to a flat file configuration (see [Flatfile configuration](#flatfile-configuration)). Must not contain path traversal (`..`). |
| `sleep_for_race` | int | Debug parameter: adds artificial delay (seconds) before pool updates to simulate race conditions. Do not use in production. |

## Operator Configuration Examples

The operator binary (`whereabouts-operator`) uses a single `controller`
subcommand that runs both reconcilers (leader-elected) and the webhook server
(all replicas) from the same process:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: whereabouts-controller-manager
spec:
  replicas: 2
  template:
    spec:
      containers:
      - name: manager
        image: ghcr.io/k8snetworkplumbingwg/whereabouts:latest
        command:
        - /whereabouts-operator
        - controller
        # Reconciliation interval (default: 30s)
        - --reconcile-interval=60s
        # Health and metrics endpoints
        - --health-probe-bind-address=:8081
        - --metrics-bind-address=:8080
        # Webhook server (all replicas serve webhooks)
        - --webhook-port=9443
        - --cert-dir=/var/run/webhook-certs
        - --namespace=kube-system
```

For the complete kustomize-based installation, run `make deploy`.
See `config/manager/manager.yaml` for the full Deployment manifest.

## L3/Routed Mode (`enable_l3`)

In pure L3/routed environments (BGP, ECMP, etc.), there is no broadcast domain.
Every IP address in the subnet is individually routable — there are no special
"network address" or "broadcast address" IPs that need to be reserved.

By default, Whereabouts reserves the first and last IP in each subnet (network
and broadcast addresses) for L2 compatibility. When `enable_l3` is set to `true`,
all IPs in the subnet become allocatable, including `.0` and `.255` addresses
(or their IPv6 equivalents).

### When to use L3 mode

- **BGP-routed pod networks**: Each pod IP is announced via BGP; no gateway needed.
- **/31 point-to-point links**: RFC 3021 subnets work out of the box.
- **/32 loopback IPs**: Single-host allocations for BGP peering or VIPs.
- **No gateway required**: L3 pools do not require a `gateway` parameter.

### Configuration

Set `enable_l3` at the top level to apply to all ranges:

```json
{
  "ipam": {
    "type": "whereabouts",
    "enable_l3": true,
    "range": "10.0.0.0/24"
  }
}
```

Or set it per range in `ipRanges` for mixed L2/L3 setups:

```json
{
  "ipam": {
    "type": "whereabouts",
    "ipRanges": [
      {"range": "10.0.0.0/24", "enable_l3": true},
      {"range": "192.168.1.0/24"}
    ]
  }
}
```

In the example above, `10.0.0.0/24` allocates all 256 IPs (`.0` through `.255`),
while `192.168.1.0/24` uses the standard L2 range (`.1` through `.254`).

## Gateway IP Exclusion (`exclude_gateway`)

When a gateway IP is configured on an L2 network, it should never be allocated
to a pod. By setting `exclude_gateway` to `true`, Whereabouts automatically adds
the gateway IP as a `/32` (or `/128` for IPv6) exclusion to every IP range.

This is **opt-in** (default: `false`) because:
- L3/routed pools typically have no gateway.
- Some deployments manage gateway exclusion via explicit `exclude` ranges.

### Configuration

```json
{
  "ipam": {
    "type": "whereabouts",
    "range": "192.168.1.0/24",
    "gateway": "192.168.1.1",
    "exclude_gateway": true
  }
}
```

This is equivalent to manually specifying:
```json
{
  "ipam": {
    "type": "whereabouts",
    "range": "192.168.1.0/24",
    "gateway": "192.168.1.1",
    "exclude": ["192.168.1.1/32"]
  }
}
```

When `exclude_gateway` is `true` but no gateway is configured, the option has
no effect.

## Optimistic IPAM (`optimistic_ipam`)

By default, Whereabouts uses leader election to serialize IP allocation across
the cluster. While this minimizes contention, it introduces latency because only
one node can allocate at a time.

Setting `optimistic_ipam` to `true` bypasses leader election entirely. Instead,
IP allocation relies solely on Kubernetes' built-in optimistic concurrency
control (resource version checks with automatic retries). This provides:

- **Lower average allocation latency** — especially in large clusters (600+ pods).
- **Higher parallelism** — multiple nodes can attempt allocation simultaneously.
- **Trade-off**: Slightly higher retry rates under heavy concurrent allocation,
  but the exponential backoff strategy handles this gracefully.

### Configuration

```json
{
  "ipam": {
    "type": "whereabouts",
    "range": "10.0.0.0/16",
    "optimistic_ipam": true
  }
}
```

> **Note**: In benchmarks this mode significantly reduces pod attach time at
> scale. If you are experiencing slow attaches at 600+ pods, try enabling this.

## Preferred/Sticky IP Assignment

Whereabouts supports assigning a preferred IP address to a pod. When the
preferred IP is available (not already allocated and not excluded), it will be
assigned directly instead of the lowest-available IP. If the preferred IP is
unavailable, allocation falls back to the standard lowest-available behavior.

This is useful for:
- **StatefulSets** that should retain the same IP across restarts.
- **Migration scenarios** where pods need specific IPs.
- **DNS/service discovery** setups that rely on stable IPs.

### Configuration

Add a pod annotation with the desired IP:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-pod
  annotations:
    whereabouts.cni.cncf.io/preferred-ip: "192.168.1.100"
spec:
  containers:
  - name: app
    image: my-app:latest
```

The annotation value must be a valid IP address within one of the configured
ranges. If the IP is outside all configured ranges, or is already allocated to
another pod, or falls in an exclude range, the annotation is silently ignored
and the standard lowest-available allocation is used.

> **Note**: This is a *preference*, not a guarantee. The idempotent allocation
> check takes precedence — if the pod already has an allocation (e.g., from a
> previous CNI ADD), that existing allocation is returned regardless of the
> annotation.

## Small Subnet Support (/32, /31, /127, /128)

Whereabouts supports allocation from very small subnets:

| Prefix | IPs | Use Case |
|--------|-----|----------|
| `/32` (`/128`) | 1 | Single-host allocation, BGP loopback, VIPs |
| `/31` (`/127`) | 2 | RFC 3021 point-to-point links |

### /32 Example

Allocate exactly one IP address:

```json
{
  "ipam": {
    "type": "whereabouts",
    "range": "10.0.0.5/32"
  }
}
```

### /31 Example (RFC 3021 point-to-point)

Allocate from a two-address point-to-point subnet:

```json
{
  "ipam": {
    "type": "whereabouts",
    "range": "10.0.0.4/31"
  }
}
```

Both `10.0.0.4` and `10.0.0.5` are allocatable.

## Graceful Node Shutdown

When a Kubernetes node undergoes graceful shutdown, the kubelet sets
`DeletionTimestamp` on all pods before draining them. The Whereabouts IP
reconciler can optionally detect these terminating pods and proactively release
their IP allocations, making the addresses available for immediate reuse on
other nodes.

This prevents IP address leaks during:
- Graceful node shutdown / reboot events.
- `kubectl drain` operations.
- Node scaling events in autoscaled clusters.

**Because the IP may still be in use while the pod is terminating, this
behavior is disabled by default.** Enable it via the operator flag:

```bash
whereabouts-operator controller --cleanup-terminating-pods
```

When disabled (the default), IPs are only released after the pod is fully
deleted. This flag applies to both the IPPool and OverlappingRangeIPReservation
reconcilers.

## Disrupted Pod Cleanup

Pods evicted by the taint manager receive a `DisruptionTarget` condition with
reason `DeletionByTaintManager`. By default, the reconcilers treat these pods
as orphaned and release their IP allocations immediately, since the taint
manager has already decided to evict them (e.g. during sudden node failures or
NoExecute taint application).

If you need to retain IP allocations for disrupted pods (for example, to allow
disruption budgets or custom eviction controllers to re-schedule the pod before
the IP is reclaimed), disable this behavior:

```bash
whereabouts-operator controller --cleanup-disrupted-pods=false
```

When disabled, pods with the `DisruptionTarget` condition keep their IP
allocations until they are fully deleted. This flag applies to both the IPPool
and OverlappingRangeIPReservation reconcilers.

## Network Status Verification

The IPPool reconciler verifies that allocated IPs are actually present in the
pod's Multus `k8s.v1.cni.cncf.io/network-status` annotation. If an allocated
IP is not found in any non-default network entry, the allocation is considered
orphaned and removed.

This check is enabled by default and provides an additional layer of orphan
detection: even if the pod exists and is running, a missing IP in the
network-status annotation indicates a stale allocation.

**If your CNI does not populate the `network-status` annotation** (e.g. when
not using Multus, or using a custom meta-plugin), disable this check:

```bash
whereabouts-operator controller --verify-network-status=false
```

When disabled, a running pod's allocations are always considered valid
regardless of the network-status annotation contents.

## Operator Feature Flag Summary

| Flag | Default | Description |
|------|---------|-------------|
| `--cleanup-terminating-pods` | `false` | Release IPs from pods with `DeletionTimestamp` set |
| `--cleanup-disrupted-pods` | `true` | Release IPs from pods with `DisruptionTarget` condition |
| `--verify-network-status` | `true` | Check Multus network-status annotation for IP presence |
| `--reconcile-interval` | `30s` | How often to re-check IP pools for orphaned allocations |

## Webhook Configuration

The operator runs validating webhooks for IPPool, NodeSlicePool, and
OverlappingRangeIPReservation CRDs. Webhooks are served by **all replicas** of
the operator Deployment (not just the leader), providing high availability for
admission requests.

### Webhook Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--webhook-port` | `9443` | Port the webhook server listens on |
| `--cert-dir` | `/var/run/webhook-certs` | Directory for TLS certificates |
| `--webhook-service-name` | `whereabouts-webhook-service` | Name of the Service for TLS certificate DNS SAN |
| `--webhook-secret-name` | `whereabouts-webhook-cert` | Name of the Secret storing webhook TLS certificates |
| `--webhook-config-name` | `whereabouts-validating-webhook-configuration` | Name of the ValidatingWebhookConfiguration to inject CA into |

### TLS Certificate Rotation

TLS certificates for the webhook server are automatically managed using the
[cert-controller](https://github.com/open-policy-agent/cert-controller)
library. The operator:

1. Creates a self-signed CA certificate in the Secret named by
   `--webhook-secret-name`.
2. Issues a server certificate with the DNS SAN based on the Service name and
   namespace (`<service-name>.<namespace>.svc`).
3. Injects the CA bundle into the `ValidatingWebhookConfiguration` named by
   `--webhook-config-name`.
4. Automatically rotates certificates before expiry.

No manual certificate management is required.

### Failure Policy

The default webhook manifests use `failurePolicy: Ignore`. This means that if
the webhook server is unavailable (e.g. during operator upgrades), admission
requests are allowed through. This is intentional: the CNI plugin must be able
to allocate IPs even if the webhook server is temporarily down. The trade-off is
that invalid CRD modifications could slip through during brief outages —
however, the reconciler will detect and fix any inconsistencies on its next run.

### CEL matchConditions Bypass

Webhook manifests include `matchConditions` CEL expressions that bypass
validation for the CNI plugin's ServiceAccount. This is necessary because the
CNI plugin creates and updates IPPool and OverlappingRangeIPReservation CRDs as
part of normal IP allocation, and webhook validation of these high-frequency
operations would add unnecessary latency and create a circular dependency (the
webhook server needs the CNI plugin to work, and the CNI plugin needs webhook
admission to pass).

### Validation Behavior

| Webhook | Validated Fields |
|---------|-----------------|
| `IPPoolValidator` | `spec.range` must be valid CIDR; allocations must have `podRef` in `"namespace/name"` format |
| `NodeSlicePoolValidator` | `spec.range` must be valid CIDR; `spec.sliceSize` must be integer 1–128 |
| `OverlappingRangeValidator` | `spec.podRef` must be in `"namespace/name"` format |

## Operator Flag Reference

Complete list of all operator flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--metrics-bind-address` | `:8080` | Address the Prometheus metrics endpoint binds to |
| `--health-probe-bind-address` | `:8081` | Address the health/readiness probes bind to |
| `--leader-elect` | `true` | Enable leader election for reconcilers |
| `--leader-elect-namespace` | `""` | Namespace for the leader election lease (defaults to pod namespace) |
| `--namespace` | `""` | Namespace where the operator runs (required for webhook cert DNS) |
| `--reconcile-interval` | `30s` | How often to re-check IP pools for orphaned allocations |
| `--webhook-port` | `9443` | Port the webhook server listens on |
| `--cert-dir` | `/var/run/webhook-certs` | Directory for TLS certificates |
| `--webhook-service-name` | `whereabouts-webhook-service` | Service name for TLS certificate DNS SAN |
| `--webhook-secret-name` | `whereabouts-webhook-cert` | Secret storing webhook TLS certificates |
| `--webhook-config-name` | `whereabouts-validating-webhook-configuration` | ValidatingWebhookConfiguration to inject CA into |
| `--cleanup-terminating-pods` | `false` | Release IPs from pods with `DeletionTimestamp` set |
| `--cleanup-disrupted-pods` | `true` | Release IPs from pods with `DisruptionTarget` condition |
| `--verify-network-status` | `true` | Check Multus network-status annotation for IP presence |

## Debugging

### CNI Plugin Debugging

Enable debug logging in the IPAM configuration:

```json
{
  "ipam": {
    "type": "whereabouts",
    "range": "192.168.2.0/24",
    "log_file": "/var/log/whereabouts.log",
    "log_level": "debug"
  }
}
```

The log file is written on the node where the CNI plugin runs (inside the
DaemonSet pod at `/host/var/log/whereabouts.log`). To read it:

```bash
# Find the whereabouts DaemonSet pod on the target node
kubectl get pods -n kube-system -l app=whereabouts -o wide

# Read the log file
kubectl exec -n kube-system <whereabouts-pod> -- cat /host/var/log/whereabouts.log
```

### Operator Debugging

The operator uses controller-runtime's structured logging via `klog`. Increase
verbosity by adding `-v` flags to the operator Deployment:

```yaml
command:
- /whereabouts-operator
- controller
- -v=4    # Increase log verbosity (0=info, 4=debug, 8=trace)
```

View operator logs:

```bash
kubectl logs -n kube-system deploy/whereabouts-controller-manager -f
```

### Inspecting IP Pools

```bash
# List all IP pools
kubectl get ippools -A

# View allocations in a specific pool
kubectl get ippool -n kube-system <pool-name> -o jsonpath='{.spec.allocations}' | jq .

# List overlapping range reservations
kubectl get overlappingrangeipreservations -A

# List node slice pools (Fast IPAM)
kubectl get nodeslicepools -A
```

### Common Debugging Scenarios

| Scenario | Commands |
|----------|----------|
| Pod stuck without IP | Check CNI logs: `cat /var/log/whereabouts.log`, inspect IPPool for exhaustion: `kubectl get ippools -A -o yaml` |
| Orphaned IPs not cleaned | Check operator is running: `kubectl get pods -n kube-system -l app=whereabouts-controller`, verify reconcile interval: `kubectl logs -n kube-system deploy/whereabouts-controller-manager \| grep reconcil` |
| Webhook rejecting requests | Check webhook logs: `kubectl logs -n kube-system deploy/whereabouts-controller-manager \| grep webhook`, verify cert Secret exists: `kubectl get secret -n kube-system whereabouts-webhook-cert` |
| Duplicate IPs across pods | Check `enable_overlapping_ranges` is `true` (default); inspect OverlappingRangeIPReservation CRDs |
| Slow allocation at scale | Enable `optimistic_ipam` or `node_slice_size` in IPAM config; check leader election logs for contention |
