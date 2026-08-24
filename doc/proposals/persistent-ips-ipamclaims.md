# Persistent IPs for KubeVirt VMs via the IPAMClaim standard

Status: **Implemented** (whereabouts#742 targets whereabouts#557, whereabouts#500)

User-facing docs: `doc/persistent-ips.md` (ships with
[whereabouts#742](https://github.com/k8snetworkplumbingwg/whereabouts/pull/742)).

This proposal describes how Whereabouts implements the multi-network de-facto
standard `IPAMClaim` contract so that KubeVirt VirtualMachines keep a stable IP
across stop/start and live migration — the same capability OVN-Kubernetes already
provides through [kubevirt/ipam-extensions](https://github.com/kubevirt/ipam-extensions)
and the [ipamclaims](https://github.com/k8snetworkplumbingwg/ipamclaims) CRD — **without**
requiring OVN-Kubernetes and **without** any KubeVirt core API change.

# Table of contents

- [Introduction](#introduction)
  - [Goal](#goal-of-this-proposal)
  - [Non-goals](#non-goals)
- [Background: how allocation works today](#background-how-allocation-works-today)
- [The IPAMClaim contract](#the-ipamclaim-contract)
- [Design](#design)
  - [How the claim reference reaches Whereabouts](#how-the-claim-reference-reaches-whereabouts)
  - [Claim namespace](#claim-namespace)
  - [Keeping the CRs in sync](#keeping-the-crs-in-sync)
  - [Changes in Modules](#changes-in-modules)
  - [Lifecycle walk-through](#lifecycle-walk-through)
- [Hard problems / remaining risks](#hard-problems--remaining-risks)
- [Delivery plan](#delivery-plan)
- [Summary](#summary)
- [Discussions and Decisions](#discussions-and-decisions)

## Introduction

When a KubeVirt VM uses a Multus secondary network backed by Whereabouts IPAM
(e.g. `bridge` + `whereabouts`), the guest IP is tied to the lifecycle of the
`virt-launcher` **pod**. On VM stop/start or live migration the pod is recreated,
Whereabouts' IP-control-loop garbage-collects the allocation, and the VM comes back
with a different address. This breaks workloads that assume a stable IP (DNS,
firewall rules, clustering, licensing).

Upstream KubeVirt has intentionally chosen **not** to solve this in KubeVirt core.
Instead, persistent VM IPs are handled at the IPAM layer through the `IPAMClaim`
CRD: a controller (`ipam-extensions`) creates an `IPAMClaim` owned by the VM, and
the IPAM plugin persists the allocation against that claim rather than against the
pod. OVN-Kubernetes already honors these claims Whereabouts should as well.

### Goal of this proposal

- Allow a KubeVirt VM interface backed by Whereabouts to retain the **same IP**
across VM stop/start and live migration.
- Do so by implementing the existing `IPAMClaim` standard, so the solution
interoperates with `ipam-extensions` and requires no new Whereabouts-specific
API and no changes to KubeVirt.
- Remain fully backwards compatible: pods without a claim reference behave exactly
as today.

### Non-goals

- Creating or deleting `IPAMClaim` resources. That is `ipam-extensions`' job the
claim is owned by the VM and its lifecycle is managed by the controller.
- Persistent IPs for arbitrary (non-KubeVirt) pods, though the mechanism is generic
(manual claims in the workload namespace work).
- Any change to KubeVirt core (a bespoke `persistIP` field was proposed in
kubevirt/kubevirt#18636 and declined as out of scope for core — this proposal is
the maintainer-recommended alternative).

## Background: how allocation works today

- The CNI entrypoint `cmd/whereabouts.go` (`cmdAdd`/`cmdDel`) calls
`kubernetes.IPManagement(ctx, Allocate|Deallocate, config, client)` in
`pkg/storage/kubernetes/ipam.go`.
- An allocation's identity is the **pod**: `IPAMConfig.GetPodRef()` returns
`"<namespace>/<podName>"` (`pkg/types/types.go`). Reservations are recorded in
`IPPool.Spec.Allocations` (`map[index]IPAllocation{ContainerID, PodRef, IfName}`)
and, when overlapping ranges are enabled, in `OverlappingRangeIPReservation` CRs.
- The IP-control-loop (`pkg/controlloop/pod.go`, `garbageCollectPodIPs`) watches
**Pod deletions** and frees any allocation whose `PodRef` matches the deleted pod.
It already contains a StatefulSet special case: if a pod of the same
name/namespace exists and is not being deleted, it skips the cleanup.

The pod-centric identity plus delete-triggered GC is exactly why VM IPs do not
persist. The claim gives us a **pod-independent identity** to anchor the allocation to.

## The IPAMClaim contract

From `k8snetworkplumbingwg/ipamclaims` (`k8s.cni.cncf.io/v1alpha1`):

```go
type IPAMClaimSpec struct {
    Network   string // network name the allocation belongs to
    Interface string // pod interface name
}

type IPAMClaimStatus struct {
    IPs        []string           // allocated addresses (v4, v6)
    OwnerPod   *OwnerPod          // { Name string } — pod currently holding the claim
    Conditions []metav1.Condition
}
```

Expected plugin behavior:

- **CNI ADD**: if the referenced claim's `status.ips` is non-empty, **reuse** those
addresses (atomically reserve them in the pool / overlapping CR). Otherwise allocate
normally and **write** `status.ips` (and set `status.ownerPod`).
- **CNI DEL**: if the allocation is claim-backed, **do not release** the IP.
- **Release** happens when the `IPAMClaim` itself is gone — observed by a
**level-driven** reconcile against live claims, not only by a delete event
(`ipam-extensions` deletes the claim when the VM is deleted).

`IPAMClaim.status.ips`, `IPPool.Spec.Allocations`, and (when enabled)
`OverlappingRangeIPReservation` must stay in sync. Drift is a first-class
reconcile problem, not a write-once side effect. See
[Keeping the CRs in sync](#keeping-the-crs-in-sync).

## Design

### How the claim reference reaches Whereabouts

`ipam-extensions` adds an `ipam-claim-reference` attribute to the VM pod's
`k8s.v1.cni.cncf.io/networks` network-selection element (NSE) for the eligible
interface.

**Resolved (mirrors OVN-Kubernetes / ipam-extensions):** Multus does **not**
currently inject `NSE.IPAMClaimReference` into delegate IPAM CNI stdin. Production
wiring keeps the claim on the pod annotation. Whereabouts therefore:

1. Accepts `ipam-claim-reference` from CNI config when present (IPAM section,
   top-level net field, or `args.cni`) — useful for tests and future Multus
   injection.
2. If still empty, **resolves the claim from the pod's network-selection
   annotation** via the Kubernetes API, matching on interface name and/or network
   name.

`ipam-claim-reference` is a **claim name only**. Empty or malformed values are
ignored (legacy pod-scoped allocation). Cross-namespace names (`ns/name`) are
rejected.

### Claim namespace

The claim is always in the **workload (pod) namespace**. There is no
`IPAMClaimNamespace` config field and no cross-namespace lookup.

This matches `ipam-extensions` (the claim is owned by the VM in the same
namespace as the `virt-launcher` pod) and avoids a pod in namespace A pinning
allocations owned by a claim in namespace B.

Reservations still record `namespace/name` because `IPPool` objects live in the
Whereabouts namespace, not the pod namespace.

### Keeping the CRs in sync

Three objects describe the same lease:

| Object | Role |
| ------ | ---- |
| `IPPool.Spec.Allocations` | **Authoritative lease.** An IP is allocated iff it appears here. |
| `OverlappingRangeIPReservation` | Same lease, when overlapping ranges are enabled. Must not diverge from the pool row. |
| `IPAMClaim.status.ips` | **Claim intent.** The addresses this claim must keep across pod recreate. Written only after a successful pool (and overlapping) reservation. |

**Write order (CNI ADD):** reserve in `IPPool` (and overlapping CR) first, tagged
with the claim identity then patch `status.ips` / `ownerPod`. A crash between
those steps leaves a claim-tagged reservation with empty `status.ips`, which
reconcile heals by completing the status write — never by freeing the IP while
the claim still exists.

**Reuse (CNI ADD with populated `status.ips`):** validate network, interface,
address family, and pool membership. Atomically take over each address only if it
is free or already owned by **this** claim (match on `IPAMClaimRef`,
`namespace/name`). Resource-version conflicts retry. Never steal an IP owned by
a different live claim.

**Reconcile (level-driven, not only delete events):** periodically, and on
informer resync / claim add-update-delete, walk claim-tagged pool and overlapping
rows and compare them to live `IPAMClaim` objects:

- Claim **NotFound** → free the pool row **and** the overlapping CR.
- Claim **lookup error** (timeout, conflict, unavailable) → **retry do not
  release**.
- Claim exists, IP is in `status.ips`, pool row missing or untagged → re-reserve
  if free or already owned by this claim otherwise set a claim condition and do
  not steal.
- Claim exists, pool row tagged for this claim, IP **not** in `status.ips` →
  treat as incomplete persist write the IP into `status.ips` rather than free.
- Overlapping CR missing or pointing at the wrong `PodRef` while the pool row is
  claim-owned → create/update the overlapping CR to match. Never leave overlapping
  state that would reject a later take-over.

A repair must never transfer an IP that another live claim already owns.

**Claim identity on every reservation:** `IPAMClaimRef` (`<pod-namespace>/<claim-name>`).
Assignment, deallocation, overlapping-range conflict checks, and GC compare this
identity, not `PodRef` alone. Existing pod-only rows stay pod-scoped (empty
claim fields).

### Changes in Modules

1. `go.mod` — add `github.com/k8snetworkplumbingwg/ipamclaims` (API types + clientset).
2. `pkg/types/types.go` — extend `IPAMConfig` / `Net` / `IPReservation`:
   ```go
   IPAMClaimReference string // claim *name* only namespace is the pod namespace
   IPAMClaimRef       string // on reservations: "namespace/name"
   ```
   No `IPAMClaimNamespace` field on config.
3. `pkg/ipamclaim/` — claim reference resolution (CNI args + pod NSE), claim fetch
   in the pod namespace, and `status.ips` / `ownerPod` persistence.
4. `pkg/config/config.go` (`LoadIPAMConfig`) — populate the claim **name** from CNI
   stdin sources when present. No behavior change when empty.
5. `pkg/allocate/allocate.go` (`AssignIPForClaim`) — reuse by claim identity /
   preferred IPs (idempotent take-over for stop/start and migration handoff) tag
   new reservations with `IPAMClaimRef`.
6. `pkg/api/...` + CRDs — `ipamclaimref` on `IPAllocation` and
   `OverlappingRangeIPReservationSpec` (does **not** overload `PodRef`).
7. `pkg/storage/kubernetes/ipam.go` — claim-aware Allocate / Deallocate:
   - Resolve claim in the pod namespace, reuse or allocate, persist status after
     the pool/overlapping write.
   - Skip freeing claim-backed reservations on Deallocate.
   - Overlapping-range create treats AlreadyExists as claim take-over when the
     existing row is owned by the same `IPAMClaimRef` (update `PodRef` / `IfName`).
8. `pkg/controlloop` — **level-driven** claim reconcile: compare claim-tagged
   pool and overlapping reservations to live `IPAMClaim` objects (add/update/delete
   **and** periodic/resync). `garbageCollectPodIPs` skips any reservation with
   non-empty `IPAMClaimRef` (release is claim-absence, not pod delete).
9. `pkg/reconciler` — orphan GC uses the same live-claim check never frees on
   lookup error.
10. Install path — IPAMClaim CRD + RBAC for `ipamclaims` / `ipamclaims/status` in
    daemonset, Helm chart, and kind e2e setup.
11. **Tests / docs** — unit tests (config, allocate, drift repair, overlapping
    take-over) e2e covers allocate → retain across pod recreate → release on
    claim delete user docs (`doc/persistent-ips.md`).

### Lifecycle walk-through

| Event                  | Actor                         | Result                                                                                                       |
| ---------------------- | ----------------------------- | ------------------------------------------------------------------------------------------------------------ |
| VM created             | ipam-extensions               | Creates `IPAMClaim` (empty `status.ips`), owned by the VM injects `ipam-claim-reference` on the launcher pod |
| Pod ADD (first boot)   | Whereabouts                   | Resolve claim from NSE/config `status.ips` empty → allocate, write `status.ips`, set `ownerPod`, tag `ipamclaimref` |
| VM stop → pod DEL      | Whereabouts                   | Claim-backed → **keep** the IP control-loop / reconciler **skip** GC when `IPAMClaimRef` is set             |
| VM start → new pod ADD | Whereabouts                   | `status.ips` populated → **reuse** the same IP (reservation handoff to new PodRef)                           |
| Live migration         | Whereabouts                   | Target pod ADD reuses the claim IP while source still holds it `ownerPod` hands off                         |
| VM deleted             | ipam-extensions → Whereabouts | `IPAMClaim` deleted → claim controller **frees** pool / overlapping reservations                             |

## Hard problems / remaining risks

1. **Live migration overlap.** Source and target pods coexist and share the
   claim's IP for a window. Take-over is allowed only for the same `IPAMClaimRef`.
   Source DEL must not clear the target's reservation.
2. **Overlapping-range reservations** — tagged with the same claim identity as
   the pool row updated (not failed) on take-over included in drift reconcile.
3. **Idempotency under churn** (old pod terminating while new pod starts) —
   handled via `IPAMClaimRef` matching and preferred-IP take-over.
4. **Incomplete updates / drift** — pool-first writes plus level-driven
   reconcile repair `status.ips` ↔ pool ↔ overlapping divergence without stealing
   IPs owned by another live claim.
5. **Controller downtime** — because cleanup is level-driven, a missed delete
   event is healed on the next reconcile. Lookup errors never release.

## Delivery plan

Originally planned as incremental PRs shipped together in
[#742](https://github.com/k8snetworkplumbingwg/whereabouts/pull/742):

| PR  | Scope                                                                         | Status                                      |
| --- | ----------------------------------------------------------------------------- | ------------------------------------------- |
| PR0 | This design note                                                              | this document                               |
| PR1 | Vendor `ipamclaims` add `IPAMClaimReference` + config parsing                | done in #742                                |
| PR2 | Allocate path: reuse existing / persist new `status.ips`                      | done in #742                                |
| PR3 | Deallocate path: do not release claim-backed IPs on pod DEL                   | done in #742                                |
| PR4 | Control-loop: IPAMClaim-delete watcher + skip GC for claim-backed allocations | done in #742                                |
| PR5 | e2e (stop/start recreate) + docs + CRD/RBAC                                   | done in #742 migration e2e still follow-up |

## Summary

Anchor persistent allocations to the `IPAMClaim` (pod-independent, VM-owned) instead
of the pod. On ADD, resolve the claim (primarily from the pod NSE), reuse
`status.ips` when present else allocate and persist them on pod DEL, keep
claim-backed IPs release only when the claim is deleted. This makes Whereabouts
interoperable with the existing `ipam-extensions`/`IPAMClaim` machinery,
delivering persistent VM IPs on non-OVN-Kubernetes clusters with no KubeVirt API
change and full backwards compatibility.

## Discussions and Decisions

- **Reference transport** — Multus does not inject the NSE claim into delegate
  IPAM stdin today. Whereabouts accepts CNI/`args.cni` when present and otherwise
  resolves `ipam-claim-reference` (claim **name**) from the pod network-selection
  annotation (same wiring as OVN-Kubernetes / ipam-extensions).
- **Claim namespace** — Pin to the workload (pod) namespace. Drop
  `IPAMClaimNamespace` no cross-namespace claims.
- **CR sync** — `IPPool.Spec.Allocations` is the authoritative lease
  `IPAMClaim.status.ips` is the persisted claim set. Reconcile must detect and
  repair drift, including incomplete pool-vs-status updates, and must never steal
  an IP another live claim owns.
- **Overlapping CR** — Same `IPAMClaimRef` (`namespace/name`) and the same
  sync/repair rules as the pool row. Take-over updates the overlapping CR instead
  of treating the target pod as a conflict.
- **Live-migration handoff** — Target ADD reuses the claim IP source DEL does
  not free it. `ownerPod` hands off overlapping AlreadyExists is take-over for
  the same `IPAMClaimRef`.
- **Level-driven cleanup** — Do not rely only on IPAMClaim delete events.
  Reconcile claim-tagged reservations against live claims on resync/periodic
  sync. API/informer errors are retryable and must not release.
- **Reservation tagging** — Dedicated `IPAMClaimRef` on IPPool allocations and
  overlapping reservations (does not overload `PodRef`).
- **Implementation** — [whereabouts#742](https://github.com/k8snetworkplumbingwg/whereabouts/pull/742).
