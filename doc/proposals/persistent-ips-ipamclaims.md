# Persistent IPs for KubeVirt VMs via the IPAMClaim standard

Status: **Proposed** (implementation in
[whereabouts#742](https://github.com/k8snetworkplumbingwg/whereabouts/pull/742),
user-facing docs `doc/persistent-ips.md` land with that PR, not this design note).

This proposal describes how Whereabouts will implement the multi-network de-facto
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

- **CNI ADD**:
  1. Resolve the claim reference (see [How the claim reference reaches Whereabouts](#how-the-claim-reference-reaches-whereabouts)).
  2. **Validate `IPAMClaimSpec` before any pool or status write.** `spec.network`
     and `spec.interface` must match the current CNI attachment (network name and
     interface). A mismatch **fails the ADD** and must not allocate, take over, or
     update `status.ips` / `ownerPod`. The same check applies to both first
     allocation (empty `status.ips`) and reuse (populated `status.ips`).
  3. The **expected address set** is one address per IPAM range on this
     attachment. A dual-stack config expects one address of each family.
     Reconcile uses the same set from the ranges on the network named by
     `IPAMClaim.spec.network`. If that network config cannot be read, do not
     invent an address and do not publish a partial `status.ips`.
  4. Reuse before allocating. Take addresses already in `status.ips`, then pool
     rows already tagged with this claim (`IPAMClaimRef` + `IPAMClaimUID`) for
     this network. Those rows include a crash after reserve and before the
     status write. Allocate only a family that neither source already covers.
     Do not reserve a second address for a family this claim already holds.
     If the pool update conflicts, another writer reserved the row first:
     re-read and reuse that row instead of returning a different address.
  5. Publish `status.ips` / `ownerPod` only after every expected address is
     reserved in the pool and, when overlapping ranges are enabled, in the
     overlapping CR. A partial list is not complete and must not be reused as
     the persistent set.
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

Collect every present `ipam-claim-reference` from these sources, in this
**precedence** (highest first):

1. IPAM section of the CNI config
2. Top-level net field
3. `args.cni` (tests / future Multus injection)
4. Pod network-selection annotation (production path today — Multus does not
   inject `NSE.IPAMClaimReference` into delegate IPAM stdin)

Resolution rules, applied **before any pool or status write**, and used by ADD,
DEL, and reconcile whenever they read a claim name from CNI/pod (after ADD, the
reservation identity is authoritative — see below):

- A present-but-invalid value in **any** source (empty string, whitespace-only,
  non-string, `ns/name`, or other malformed) **fails immediately**. It does not
  fall through to a lower source.
- If two or more sources are present with **valid but different** claim names,
  **reject** the operation. Do not pick a winner. ADD and a later DEL/reconcile
  must not bind different claims.
- If sources agree, or only one source is present and valid, use that name.
- If the attribute is **absent** from every source, use legacy pod-scoped
  allocation.

Annotation matching (source 4), when that source is used:

- When both interface name and network name are available, match **both**.
- A single-field match (interface **or** network) is allowed only when it
  yields **exactly one** candidate.
- Ambiguous matches (the same network attached more than once, or multiple
  NSEs that satisfy a single-field filter) **fail CNI ADD**. Do not pick an
  arbitrary NSE.

`ipam-claim-reference` is a **claim name only**. Tests must cover every source
combination: each source alone, agreeing sources, conflicting valid names, and
present-but-invalid values mixed with a valid lower source.

### Claim namespace

The claim is always in the **workload (pod) namespace**. There is no
`IPAMClaimNamespace` config field and no cross-namespace lookup.

This matches `ipam-extensions` (the claim is owned by the VM in the same
namespace as the `virt-launcher` pod) and avoids a pod in namespace A pinning
allocations owned by a claim in namespace B.

Reservations record the claim as `namespace/name` **plus UID** because `IPPool`
objects live in the Whereabouts namespace, not the pod namespace, and a deleted
claim can be recreated with the same name.

### Keeping the CRs in sync

Three objects describe the same lease:

| Object | Role |
| ------ | ---- |
| `IPPool.Spec.Allocations` | **Authoritative lease.** An IP is allocated iff it appears here. |
| `OverlappingRangeIPReservation` | Same lease, when overlapping ranges are enabled. Must not diverge from the pool row. |
| `IPAMClaim.status.ips` | **Claim intent.** The addresses this claim must keep across pod recreate. Written only after a successful pool (and overlapping) reservation. |

**Write order (CNI ADD):** reserve in `IPPool` (and overlapping CR) first, tagged
with the claim identity, then patch `status.ips` / `ownerPod`. Publish
`status.ips` only when the reservation covers the full expected address set.
A crash can leave claim-tagged rows with an empty or partial `status.ips`.
That is an incomplete allocation: the next ADD or reconcile reuses the tagged
rows, allocates only the missing families, and writes status once the set is
complete. Never free those rows while the claim still exists, and never treat
one family of a dual-stack attachment as the whole claim.

**Reuse (CNI ADD with populated `status.ips`):** after the `IPAMClaimSpec`
network/interface check above, also validate address family and pool membership.
Reuse that list only when it is the full expected set. A partial list is
completed before it is treated as the persistent set. Atomically take over each
address only if it is free or already owned by **this** claim (match on
`IPAMClaimRef` + `IPAMClaimUID`). Resource-version conflicts retry. Never steal
an IP owned by a different live claim.

**Reconcile (level-driven, not only delete events):** periodically, and on
informer resync / claim add-update-delete, walk the **union** of (1) live
`IPAMClaim` objects and their `status.ips`, (2) claim-tagged `IPPool` rows, and
(3) overlapping-range reservations. Starting from reservations alone cannot see
a live claim whose `status.ips` has no pool row. Starting from claims alone
cannot see overlap-only leftovers.

- Claim **NotFound** for a tagged pool or overlapping row → free that row.
- Claim **lookup error** (timeout, conflict, unavailable) → **retry, do not
  release**.
- Live claim, IP in `status.ips`, pool row missing or untagged → re-reserve
  if free or already owned by this claim (UID match).
- Live claim, IP in `status.ips`, pool row owned by a **different** live claim
  (different `IPAMClaimRef` or `IPAMClaimUID`) → do not steal, free, or rewrite
  that row, and do not drop the IP from this claim's `status.ips`. This is not
  a retryable API error. Set `status.conditions` on **this** claim:
  `type: IPConflict`, `status: "False"`, `reason: OwnedByOtherClaim`, with a
  message naming the IP and the other claim's `namespace/name` and UID.
  Reconcile records the condition and moves on. CNI ADD for this claim fails
  closed on that address and does not allocate a replacement, because
  `status.ips` is the persistent set. The pod stays unready until an operator
  resolves the conflict.
- Live claim, pool rows tagged for this claim (UID match), `status.ips`
  missing one or more addresses from the expected set → incomplete allocation.
  Do not publish the partial list, and do not let ADD reuse it as the full
  set. Allocate only the missing families, then write `status.ips` once every
  expected address is reserved. A crash after IPv4 and before IPv6 is this
  case.
- Overlapping CR vs pool row: compare the **complete lease identity** — IP,
  `IPAMClaimRef`, `IPAMClaimUID`, `PodRef`, `IfName`, and the pool/network key.
  - The overlapping row is missing, or it diverges and is **not** a live
    pod-scoped allocation → create or update it to match the claim-owned pool
    row. A same-claim take-over stays idempotent.
  - The overlapping row is a **live pod-scoped** allocation (empty claim
    fields, pod still present), on ADD or later in reconcile → do not
    overwrite or delete it. Fail the claim allocation and do not publish
    `status.ips`. Set `IPConflict` / `OverlappingPodAllocation` on this claim,
    naming the IP and the pod. If this claim already created a pool row and
    `status.ips` does not list that IP, delete that pool row only after
    confirming its `IPAMClaimRef` and `IPAMClaimUID` match this claim. Never
    delete a row owned by the pod or by another claim.
- **Overlap-only / stale overlap:** a **claim-tagged** overlapping row with no
  matching claim-owned pool row, or whose claim is gone, is **deleted**. Do
  not keep claim overlap state that would block a later allocation. Never
  remove or overwrite an overlapping row owned by a different live claim or
  by a live pod-scoped allocation.

A repair must never transfer an IP that another live claim already owns.

**Claim identity on every reservation:** `IPAMClaimRef`
(`<pod-namespace>/<claim-name>`) **and** `IPAMClaimUID` (the claim object's
UID). Assignment, deallocation, overlapping-range conflict checks, and GC
compare both. A claim deleted and recreated with the same name is a different
object and must not match a stale reservation.

Existing **pod-only** rows (empty claim fields) stay pod-scoped.

**Name-only claim rows** (upgrade from an earlier `IPAMClaimRef` without UID):
on the first successful ADD or reconcile against a live claim of that
namespace/name, stamp `IPAMClaimUID`. If no live claim exists, free the row.
Do not treat a recreated same-name claim as the owner of a UID-stamped row
for a previous UID. Operators should not delete/recreate claims during that
one-time stamp.

### Changes in Modules

1. `go.mod` — add `github.com/k8snetworkplumbingwg/ipamclaims` (API types + clientset).
2. `pkg/types/types.go` — extend `IPAMConfig` / `Net` / `IPReservation`:
   ```go
   IPAMClaimReference string // claim *name* only, namespace is the pod namespace
   IPAMClaimRef       string // on reservations: "namespace/name"
   IPAMClaimUID       string // on reservations: claim object UID
   ```
   No `IPAMClaimNamespace` field on config.
3. `pkg/ipamclaim/` — claim reference resolution (precedence + conflict reject
   across CNI fields and pod NSE, unambiguous NSE matching), fail-closed
   validation of present-but-invalid references, claim fetch in the pod
   namespace, `IPAMClaimSpec` network/interface validation against the current
   attachment, and `status.ips` / `ownerPod` persistence.
4. `pkg/config/config.go` (`LoadIPAMConfig`) — populate the claim **name** from CNI
   stdin sources when present, applying the same precedence/conflict rules.
   Absent attribute: no behavior change. Present but invalid: fail config load.
5. `pkg/allocate/allocate.go` (`AssignIPForClaim`) — reuse by claim identity
   (name + UID) / preferred IPs (idempotent take-over for stop/start and
   migration handoff), tag new reservations with `IPAMClaimRef` + `IPAMClaimUID`.
6. `pkg/api/...` + CRDs — `ipamclaimref` and `ipamclaimuid` on `IPAllocation` and
   `OverlappingRangeIPReservationSpec` (does **not** overload `PodRef`).
7. `pkg/storage/kubernetes/ipam.go` — claim-aware Allocate / Deallocate:
   - Resolve claim in the pod namespace, validate `IPAMClaimSpec` against the
     current attachment, then reuse or allocate, persist status after the
     pool/overlapping write.
   - Skip freeing claim-backed reservations on Deallocate.
   - Overlapping-range create treats AlreadyExists as claim take-over when the
     existing row is owned by the same `IPAMClaimRef` **and** UID (update
     `PodRef` / `IfName`).
8. `pkg/controlloop` — **level-driven** claim reconcile over the union of live
   claims, pool rows, and overlapping rows (add/update/delete **and**
   periodic/resync), including overlap-only cleanup. `garbageCollectPodIPs`
   skips any reservation with non-empty `IPAMClaimRef` (release is
   claim-absence, not pod delete).
9. `pkg/reconciler` — orphan GC uses the same live-claim + UID check, never
   frees on lookup error.
10. Install path — IPAMClaim **CRD + RBAC** (`ipamclaims` / `ipamclaims/status`)
    in daemonset, Helm chart, and kind e2e setup. This ships in the **same
    rollout unit** as claim-aware allocate/deallocate/control-loop, not after
    it. If the CRD or status permissions are missing, claim-aware paths must
    not run (fail ADD / skip reconcile writes), do not allocate as if the
    feature were enabled.
11. **Tests / docs** — unit tests (source precedence and conflict combinations,
    config fail-closed parsing, unambiguous NSE match, spec validation, UID
    identity, name-only migration, bidirectional drift repair, overlap-only
    removal, overlapping take-over). e2e covers allocate → retain across pod
    recreate → release on claim delete. User docs (`doc/persistent-ips.md`)
    ship with the implementation PR, not this design note. Live-migration e2e
    remains follow-up.

### Lifecycle walk-through

| Event                  | Actor                         | Result                                                                                                       |
| ---------------------- | ----------------------------- | ------------------------------------------------------------------------------------------------------------ |
| VM created             | ipam-extensions               | Creates `IPAMClaim` (empty `status.ips`), owned by the VM injects `ipam-claim-reference` on the launcher pod |
| Pod ADD (first boot)   | Whereabouts                   | Resolve + validate claim/spec, allocate, tag `ipamclaimref` + UID, write `status.ips` / `ownerPod`           |
| VM stop → pod DEL      | Whereabouts                   | Claim-backed → **keep** the IP control-loop / reconciler **skip** GC when `IPAMClaimRef` is set             |
| VM start → new pod ADD | Whereabouts                   | Validate spec, `status.ips` populated → **reuse** the same IP (reservation handoff to new PodRef)            |
| Live migration         | Whereabouts                   | Target pod ADD reuses the claim IP while source still holds it `ownerPod` hands off                         |
| VM deleted             | ipam-extensions → Whereabouts | `IPAMClaim` deleted → claim controller **frees** pool / overlapping reservations                             |

## Hard problems / remaining risks

1. **Live migration overlap.** Source and target pods coexist and share the
   claim's IP for a window. Take-over is allowed only for the same `IPAMClaimRef`
   **and** UID. Source DEL must not clear the target's reservation.
2. **Overlapping-range reservations** — tagged with the same claim identity as
   the pool row. Same-claim take-over updates the row. A live pod-scoped
   overlapping reservation is left in place and the claim allocation fails.
   Claim-tagged overlap-only stale rows are removed.
3. **Idempotency under churn** (old pod terminating while new pod starts) —
   handled via `IPAMClaimRef` + UID matching and preferred-IP take-over.
4. **Incomplete updates / drift** — pool-first writes plus level-driven
   reconcile over the union of claims, pool, and overlapping rows. A partial
   dual-stack reservation is completed before `status.ips` is published.
   Repair never steals an IP owned by another live claim or a live pod.
5. **Claim recreate** — same name, new UID must not inherit a stale reservation.
   Name-only rows are stamped once during upgrade.
6. **Controller downtime** — because cleanup is level-driven, a missed delete
   event is healed on the next reconcile. Lookup errors never release.

## Delivery plan

Originally planned as incremental PRs. **PR1–PR4 plus CRD/RBAC are one
compatible rollout unit** and must not land independently: persisting
`status.ips` without the DEL and GC protections would leave a claim holding an
address after the pool record is freed, and enabling claim resolution/watches
without the CRD and `ipamclaims`/`ipamclaims/status` RBAC would fail lookups or
status updates while allocation code is on.

**Upgrade order:** install CRD and RBAC first, then start claim-aware binaries.
**Rollback** removes the feature. Persistent IPs are released and may change
afterward. Do not roll the binaries back while claim-backed leases are still
active: legacy DEL and GC would free the pool row while `status.ips` still
names it.

1. Stop the affected workloads so they do not recreate claims or new
   claim-backed allocations.
2. Delete the `IPAMClaim` objects. Leave the claim-aware controller running
   until it frees their pool rows and overlapping reservations.
3. Confirm that no claim-tagged `IPPool` rows or claim-tagged
   `OverlappingRangeIPReservation` objects remain. If that cannot be verified,
   stop. Pod-scoped allocations and the Whereabouts CRDs stay.
4. Roll back the Whereabouts binaries.
5. Remove the `IPAMClaim` CRD and its RBAC only after no claims remain. Do not
   remove the `IPPool` or overlapping-range CRDs.

If the CRD or status permissions are absent while claim-aware binaries are
still running, gate the feature off (fail closed on claim-backed ADD, do not
treat a missing CRD as legacy allocation when a claim reference is present).

Implementation is tracked in
[#742](https://github.com/k8snetworkplumbingwg/whereabouts/pull/742) (open, not
shipped). Live-migration e2e remains follow-up.

| PR  | Scope                                                                         | Status                                      |
| --- | ----------------------------------------------------------------------------- | ------------------------------------------- |
| PR0 | This design note                                                              | this document                               |
| PR1 | Vendor `ipamclaims`, add `IPAMClaimReference` + config parsing, **CRD + RBAC** | in #742 (not merged)                        |
| PR2 | Allocate path: reuse existing / persist new `status.ips`                      | in #742 (not merged)                        |
| PR3 | Deallocate path: do not release claim-backed IPs on pod DEL                   | in #742 (not merged)                        |
| PR4 | Control-loop: IPAMClaim reconcile + skip GC for claim-backed allocations      | in #742 (not merged)                        |
| PR5 | e2e (stop/start recreate) + user docs                                         | in #742 (not merged), migration e2e follow-up |

## Summary

Anchor persistent allocations to the `IPAMClaim` (pod-independent, VM-owned)
instead of the pod. On ADD, resolve the claim (primarily from the pod NSE),
validate `spec.network` / `spec.interface`, and reuse `status.ips` when present.
Otherwise, allocate and persist the IPs. On pod DEL, keep claim-backed IPs.
Release them only when the claim is deleted. This makes Whereabouts interoperable
with the existing `ipam-extensions`/`IPAMClaim` machinery, delivering persistent
VM IPs on non-OVN-Kubernetes clusters with no KubeVirt API change and full
backwards compatibility.

## Discussions and Decisions

- **Reference transport** — Multus does not inject the NSE claim into delegate
  IPAM stdin today. Sources, highest precedence first: IPAM section, top-level
  net field, `args.cni`, then the pod network-selection annotation. Present-but-
  invalid values fail immediately. Distinct valid names from two sources are
  rejected. NSE matching uses both interface and network when both are set, a
  single-field match is valid only when unique, ambiguity fails CNI ADD. Tests
  cover every source combination.
- **Fail closed** — Only an **absent** `ipam-claim-reference` uses legacy
  pod-scoped allocation. A present-but-invalid or cross-namespace value is a
  validation error, not a silent fallback.
- **Claim spec validation** — Before first allocation and before reuse, require
  `IPAMClaimSpec.Network` and `Interface` to match the current CNI attachment.
  Mismatch fails ADD with no pool or claim-status change.
- **Claim namespace** — Pin to the workload (pod) namespace. Drop
  `IPAMClaimNamespace`, no cross-namespace claims.
- **CR sync** — `IPPool.Spec.Allocations` is the authoritative lease,
  `IPAMClaim.status.ips` is the persisted claim set. Reconcile walks the **union**
  of live claims, pool rows, and overlapping rows so a claim with no pool row
  is still repaired. Never steal an IP another live claim owns. If `status.ips`
  names an address whose pool row belongs to a different live claim, set
  `IPConflict` / `OwnedByOtherClaim` on this claim and fail ADD closed; do not
  retry, release, or allocate a substitute. Publish `status.ips` only for the
  full expected set (one address per IPAM range); finish a missing family
  before reuse. Claim-tagged overlap-only stale rows are deleted.
- **Overlapping CR** — Same claim identity (`namespace/name` + UID) and the same
  sync/repair rules as the pool row, comparing the complete lease identity (IP,
  `IPAMClaimRef`, `IPAMClaimUID`, `PodRef`, `IfName`, pool/network key).
  Same-claim take-over updates the overlapping CR. A live pod-scoped
  overlapping reservation is not overwritten; the claim allocation fails with
  `IPConflict` / `OverlappingPodAllocation`.
- **Live-migration handoff** — Target ADD reuses the claim IP, source DEL does
  not free it. `ownerPod` hands off, overlapping AlreadyExists is take-over for
  the same `IPAMClaimRef` + UID.
- **Level-driven cleanup** — Do not rely only on IPAMClaim delete events.
  Reconcile the union of live claims and both reservation stores on
  resync/periodic sync. API/informer errors are retryable and must not release.
- **Reservation tagging** — Dedicated `IPAMClaimRef` + `IPAMClaimUID` on IPPool
  allocations and overlapping reservations (does not overload `PodRef`). Name-only
  rows are a one-time upgrade stamp against the live claim of that name.
- **Rollout** — Allocate-path persist (`status.ips`) must not ship without DEL
  and GC protections, and claim-aware code must not ship without the IPAMClaim
  CRD and RBAC. Upgrade: CRD/RBAC first, then binaries. Rollback: delete
  claims and wait until their reservations are gone, then binaries, then the
  `IPAMClaim` CRD. The CRD stays until no claims remain. PR1–PR4 (including
  install path) are one rollout unit in
  [whereabouts#742](https://github.com/k8snetworkplumbingwg/whereabouts/pull/742)
  (open). Live-migration e2e is follow-up.
