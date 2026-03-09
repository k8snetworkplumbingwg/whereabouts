# Proposal: Migrate Whereabouts to controller-runtime v0.23.2 with SSA

## TL;DR

Replace the hand-rolled `ip-control-loop` and `node-slice-controller` with a single controller-runtime v0.23.2 operator binary using Cobra subcommands (`controller` and `webhook`), following the team's auth-operator pattern. The DaemonSet is slimmed to CNI-install + token-watcher only. Validating webhooks are added for all three CRDs, with `matchConditions` CEL bypass for the whereabouts service account (so the CNI binary is never blocked). Certificate management uses `github.com/open-policy-agent/cert-controller/pkg/rotator`. Field indexers are used throughout for efficient cross-resource lookups. SSA for `NodeSlicePool` and `OverlappingRangeIPReservation`; JSON Patch with optimistic locking for `IPPool` allocations. All tests migrated to Ginkgo v2. P0/P1 bugs from review.md fixed. E2e tests continue to pass unchanged. An `AGENTS.md` is created based on the kubebuilder v4.11.0 template.

## Architecture

| Component | Before | After |
|-----------|--------|-------|
| CNI binary | `/whereabouts` on host | Unchanged |
| Pod cleanup + reconciler | `/ip-control-loop` in DaemonSet | `/whereabouts-operator controller` in Deployment |
| Fast IPAM controller | `/node-slice-controller` in Deployment | Merged into `/whereabouts-operator controller` |
| Webhook validation | None | `/whereabouts-operator webhook` in Deployment |
| DaemonSet | `install-cni.sh` + `token-watcher.sh` + `ip-control-loop` | `install-cni.sh` + `token-watcher.sh` only |
| Cert management | N/A | `cert-controller/pkg/rotator` (self-signed, auto-rotation) |

## Review.md Issues Resolved

| Finding | How |
|---------|-----|
| **P2-1** 10s `requestCtx` defeats 100-retry loop | Per-retry `context.WithTimeout` inside RETRYLOOP |
| **P2-2** `nil` leader elector panic | Nil guard before goroutine |
| **P2-3** Shared `err` across loop iterations | Scope with `:=` per ipRange |
| **P2-4** `skipOverlappingRangeUpdate` not reset | Reset at top of each ipRange |
| **P5-2** No health/readiness/liveness probes | controller-runtime Manager `/healthz` `/readyz` |
| **P5-3** No metrics or observability | controller-runtime Prometheus metrics at `/metrics` |
| **P5-6** Reconciler runs without context/timeout | controller-runtime passes ctx to `Reconcile()` |
| **P6-2** Global logging state not thread-safe | `logr.Logger` — structured, per-reconciler |
| **P9-4** Node-slice-controller has no leader election | controller-runtime Manager handles it |
| **P10-2** `checkForMultiNadMismatch` O(n²) | Eliminated; field indexers |
| **P10-3** Reconciler loads ALL pods & pools | Cache-backed indexed lookups |
| **P11-1** Partial multi-range not rolled back | Compensating deallocations on failure |
| **P11-2** Corrupt annotation causes mass cleanup | Skip pod on parse error, don't treat as orphan |
| **P11-3** IPv6 normalization mismatch | `net.IP.Equal()` instead of string comparison |
| **P11-4** Reconciler snapshot stale by cleanup time | Event-driven reconciliation replaces batch snapshot |
| **P11-5** `close(errorChan)` race | Eliminated — controller-runtime lifecycle |

## Implementation Steps

### Step 1: Bump dependencies

Update `go.mod`:
- Bump `k8s.io/api`, `k8s.io/apimachinery`, `k8s.io/client-go`, `k8s.io/code-generator` from v0.34.1 → v0.35.0
- Add `sigs.k8s.io/controller-runtime v0.23.2`
- Add `github.com/open-policy-agent/cert-controller` (latest compatible)
- Add `github.com/spf13/cobra` (for subcommands)
- Replace `github.com/onsi/ginkgo` v1 → `github.com/onsi/ginkgo/v2`
- Run `go mod tidy && go mod vendor && go mod verify`

### Step 2: Generate ApplyConfiguration types

Modify `hack/update-codegen.sh`: add `--with-applyconfig` to `kube::codegen::gen_client`. Run codegen. This generates `pkg/generated/applyconfiguration/` and updates typed clientset with `Apply()` methods. Verify with `./hack/verify-codegen.sh`.

### Step 3: Update CRD scheme registration

Update `api/v1alpha1/register.go` to ensure the `SchemeBuilder` and `AddToScheme` work with controller-runtime's scheme. Register NAD types in the same scheme for the operator.

### Step 4: Migrate all tests Ginkgo v1 → v2

Replace `github.com/onsi/ginkgo` → `github.com/onsi/ginkgo/v2` in all test files. Remove Ginkgo v1 from go.mod. Files:
- `cmd/whereabouts_test.go`
- `pkg/allocate/allocate_test.go`
- `pkg/config/config_test.go`
- `pkg/iphelpers/iphelpers_test.go`
- `e2e/e2e_test.go`
- `e2e/e2e_node_slice/e2e_node_slice_test.go`
- `e2e/poolconsistency/poolconsistency_test.go`
- All test files in `pkg/controlloop/` and `pkg/node-controller/`

### Step 5: Create operator entry point with Cobra subcommands

Create `cmd/operator/main.go` with Cobra root command and a single `controller` subcommand:

**`controller` subcommand**:
- `ctrl.Manager` with leader election, health/ready probes (`:8081`), Prometheus metrics (`:8080`)
- All field indexers (Step 6)
- All reconcilers (Steps 7–8)
- Embedded webhook server on `:9443` with cert-controller TLS rotation
- All replicas serve webhooks; only the leader runs reconcilers
- Flags: `--reconcile-interval` (default 30s), `--metrics-bind-address`, `--health-probe-bind-address`, `--leader-elect-namespace`, `--webhook-port`, `--cert-dir`, `--namespace`, `--log-level`

### Step 6: Register field indexers

| Index | Object | Extract Function | Used By |
|-------|--------|-----------------|---------|
| `spec.allocations.podref` | `IPPool` | All unique `podRef` values from `spec.allocations` | IPPoolReconciler Pod→IPPool mapping |
| `spec.podref` | `OverlappingRangeIPReservation` | `spec.podref` field | OverlappingRangeReconciler Pod→Reservation mapping |
| `status.allocations.nodeName` | `NodeSlicePool` | All `nodeName` from `status.allocations` | NodeSlicePoolReconciler Node→Pool |
| `spec.range` | `IPPool` | `spec.range` field | Validation: detect duplicate ranges |

### Step 7: IPPoolReconciler

Create `internal/controller/ippool_controller.go`. Replaces both PodController and gocron reconciler.

**Watches:**
- `For(&v1alpha1.IPPool{})` with predicate: trigger when `spec.allocations` changes
- `Watches(&v1.Pod{}, handler.EnqueueRequestsFromMapFunc(podToIPPoolMapper))` — Pod delete events, mapper uses `spec.allocations.podref` index
- `Watches(&v1alpha1.OverlappingRangeIPReservation{}, handler.EnqueueRequestsFromMapFunc(overlappingToIPPoolMapper))`

**Reconcile logic:**
1. Get IPPool; if NotFound → return
2. For each allocation: check pod exists, check IP on pod using `net.IP.Equal()` (fixes P11-3), handle parse errors gracefully (skip pod — fixes P11-2), handle Pending pods with `RequeueAfter(5s)`, handle `DisruptionTarget` condition
3. Remove orphaned allocations via JSON Patch with `client.RawPatch(types.JSONPatchType, ...)` — optimistic locking with `resourceVersion` test ops
4. Clean up corresponding OverlappingRangeIPReservation CRDs
5. Emit K8s Event on IPPool
6. Return `RequeueAfter(reconcileInterval)`

**Why NOT SSA:** IPPool `spec.allocations` map is concurrently modified by the CNI binary and reconciler. SSA would require `ForceOwnership` over the entire map, creating a race with CNI ADD.

### Step 8: NodeSlicePoolReconciler and OverlappingRangeReconciler

**NodeSlicePoolReconciler** — `internal/controller/nodeslicepool_controller.go`:
- Watches: `NetworkAttachmentDefinition` (primary), `Node` (secondary → enqueue NADs), `NodeSlicePool` (owned via owner ref)
- Reconcile: parse NAD IPAM config → if `node_slice_size` → list Nodes → compute slices → **SSA Apply** NodeSlicePool with generated `ApplyConfiguration` types
- Sets owner reference NAD→NodeSlicePool

**OverlappingRangeReconciler** — `internal/controller/overlappingrange_controller.go`:
- Watches: `OverlappingRangeIPReservation` (primary), `Pod` via `EnqueueRequestsFromMapFunc` using `spec.podref` index
- Reconcile: check if `spec.podRef` pod exists; if not → `client.Delete()`
- Periodic `RequeueAfter(reconcileInterval)`

### Step 9: Webhook cert-controller wrapper

Create `internal/webhook/certrotator/certrotator.go` — thin wrapper:
```go
func Enable(mgr manager.Manager, namespace, certDir, dnsName, secretName string,
    webhooks []rotator.WebhookInfo, ready chan struct{}) error
```
Calls `rotator.AddRotator(mgr, &rotator.CertRotator{...})` with:
- `CAOrganization: "telekom"`, `CAName: "whereabouts-ca"`
- `RequireLeaderElection: true`, `RestartOnSecretRefresh: true`
- `Webhooks: []rotator.WebhookInfo{{Name: "whereabouts-validating-webhook", Type: rotator.Validating}}`

RBAC markers:
```go
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingwebhookconfigurations,verbs=get;list;watch;update;patch
```

### Step 10: Validating webhooks for CRDs

**`internal/webhook/ippool_webhook.go`** — `webhook.CustomValidator`:
- ValidateCreate: valid CIDR, non-negative offsets within range, valid podRef format, no duplicate podRef+ifName
- ValidateUpdate: + `spec.range` immutability
- ValidateDelete: no-op

**`internal/webhook/nodeslicepool_webhook.go`** — `webhook.CustomValidator`:
- ValidateCreate: valid CIDR, valid slice size, slices fit in range, no duplicate nodes, no overlapping slices
- ValidateUpdate: + `spec.range` and `spec.sliceSize` immutability
- ValidateDelete: no-op

**`internal/webhook/overlappingrange_webhook.go`** — `webhook.CustomValidator`:
- ValidateCreate: non-empty valid podRef, valid IP from name
- ValidateUpdate: `spec` immutability
- ValidateDelete: no-op

**CNI bypass via matchConditions:**
```yaml
matchConditions:
- name: exclude-whereabouts-sa
  expression: >-
    !("system:serviceaccount:kube-system:whereabouts" in
    [request.userInfo.username])
```

### Step 11: Comprehensive unit tests

All controllers and webhooks get Ginkgo v2 + Gomega tests. Use `envtest.Environment` for integration and fake client for fast edge cases.

**ippool_controller_test.go:**
- Good: valid → no-op; orphan removed + event; multiple orphans; Pod delete via index; empty pool; periodic requeue
- Edge: Pending → requeue 5s; no annotation → requeue; DisruptionTarget → orphaned; IPv6 normalization; malformed annotation → skip; deleted pool → no error; dual-interface; non-numeric offset → skip
- Failure: API error → requeue; conflict → requeue; context cancelled

**nodeslicepool_controller_test.go:**
- Good: NAD with slice_size → SSA create; Node add/remove; NAD update
- Edge: NAD deleted → GC; zero nodes; more nodes than slices; invalid IPAM JSON
- Failure: SSA conflict; API unreachable

**overlappingrange_controller_test.go:**
- Good: valid podRef → no-op; non-existent → deleted
- Edge: Pending → requeue; already deleted → ignore
- Failure: Delete error → requeue

**Webhook tests:** Valid → accepted; each invalid field combination → rejected with specific error.

### Step 12: Fix CNI storage layer bugs

In `pkg/storage/kubernetes/ipam.go`:
- **P2-1**: Per-retry `context.WithTimeout` inside RETRYLOOP
- **P2-2**: Nil guard on leader elector
- **P2-3**: Scope `err` per ipRange with `:=`
- **P2-4**: Reset `skipOverlappingRangeUpdate` per ipRange
- **P11-1**: Compensating deallocations on partial multi-range failure

### Step 13: Update build system

**make build:**
- Build `bin/whereabouts-operator` from `./cmd/operator/`
- Remove `bin/ip-control-loop` and `bin/node-slice-controller`

**Dockerfile:**
- Build `whereabouts-operator` (replaces both old binaries)

**Makefile:**
- Update `test` to include `./internal/...`
- Add `install-envtest` target

### Step 14: Update Kubernetes manifests

**DaemonSet** (`config/daemonset/daemonset.yaml`):
- Command: `SLEEP=false source /install-cni.sh && /token-watcher.sh` (foreground)
- Remove `ip-control-loop` invocation and ConfigMap cron mount
- Split RBAC: DaemonSet SA → minimal CNI permissions only

**Operator Controller+Webhook Deployment** (`config/manager/manager.yaml`):
- `replicas: 2`, leader election for reconcilers
- All replicas serve webhooks on `:9443`
- Liveness `:8081/healthz`, readiness `:8081/readyz`, metrics `:8080`
- Label `control-plane: controller-manager`
- Cert volume from Secret `whereabouts-webhook-cert`

**ValidatingWebhookConfiguration** (`config/webhook/manifests.yaml` + kustomize patches):
- IPPool, NodeSlicePool, OverlappingRangeIPReservation rules
- `matchConditions` CEL bypass for `whereabouts` SA
- `failurePolicy: Fail`
- `cert-controller.io/inject-ca-from` annotation

**Removed:** `doc/crds/` manual install manifests (replaced by kustomize `config/` tree)

**Helm chart:**
- Add `webhook.enabled` (default `false`)
- Add operator Deployment/Service/RBAC templates
- Update DaemonSet: remove ip-control-loop
- Add `values.yaml` options for replicas, interval, ports

### Step 15: Slim down DaemonSet

- Remove `ip-control-loop` from container command
- Command becomes: `SLEEP=false source /install-cni.sh && /token-watcher.sh`
- Remove ConfigMap volume mount for cron
- Minimal RBAC (CRD CRUD for CNI binary, pods get, leases for leader election)

### Step 16: Delete old code

- `cmd/controlloop/`, `cmd/nodeslicecontroller/`
- `pkg/controlloop/`, `pkg/node-controller/`, `pkg/reconciler/`
- go.mod removals: `gocron/v2`, `fsnotify`, `ginkgo` v1
- Keep `pkg/generated/` (CNI binary still uses it)

### Step 17: Create AGENTS.md

Repository root, kubebuilder v4.11.0 template adapted for Whereabouts structure.

## Verification

1. `go build ./cmd/...` — all binaries compile
2. `go test ./internal/... ./pkg/... -v -count=1` — all tests pass
3. `make test` — full suite passes
4. `make kind && cd e2e && go test -v . -timeout=1h` — e2e unchanged
5. Operator probes, metrics, leader election, orphan cleanup, webhook validation, CNI bypass, cert rotation

## Key Decisions

- **Cobra subcommands** (`controller`/`webhook`): Follow auth-operator pattern
- **cert-controller**: Self-signed, auto-rotating, no cert-manager dependency
- **`matchConditions` CEL bypass**: GA since k8s 1.30, safe with v0.35.0
- **DaemonSet slim-down**: CNI install + token-watcher only
- **Two Deployments**: Controller (leader-elected) + Webhook (Service-backed)
- **Webhooks disabled by default**: `webhook.enabled: false` in Helm
- **`internal/` layout**: kubebuilder convention, private to operator binary
- **Field indexers**: All cross-resource lookups indexed
- **SSA for NodeSlicePool, JSON Patch for IPPool**: SSA unsafe for shared allocations map
- **RBAC split**: DaemonSet SA minimal, operator SA comprehensive
- **`RequeueAfter` default 30s**: Replaces gocron, fast enough for e2e tests
