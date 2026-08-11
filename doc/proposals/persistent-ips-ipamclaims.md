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
pod. OVN-Kubernetes already honors these claims Whereabouts now does as well
([#742](https://github.com/k8snetworkplumbingwg/whereabouts/pull/742)).

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
(manual claims work; see `doc/persistent-ips.md` in #742).
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
addresses. Otherwise allocate normally and **write** `status.ips` (and set
`status.ownerPod`).
- **CNI DEL**: if the allocation is claim-backed, **do not release** the IP.
- **Release** happens only when the `IPAMClaim` itself is deleted (which
`ipam-extensions` triggers when the VM is deleted).

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
   annotation** via the Kubernetes API (`pkg/ipamclaim.ResolveFromPodAnnotation`),
   matching on interface name and/or network name.

Claim namespace defaults to the pod namespace.

### Changes in Modules

As implemented in [#742](https://github.com/k8snetworkplumbingwg/whereabouts/pull/742):

1. `go.mod` — add `github.com/k8snetworkplumbingwg/ipamclaims` (API types + clientset).
2. `pkg/types/types.go` — extend `IPAMConfig` / `Net` / `IPReservation`:
   ```go
   IPAMClaimReference string // name of the IPAMClaim, empty => legacy behavior
   IPAMClaimNamespace string // usually the pod namespace
   IPAMClaimRef       string // on reservations: "namespace/name"
   ```
3. `pkg/ipamclaim/` — claim reference resolution (CNI args + pod NSE), claim fetch,
   and `status.ips` / `ownerPod` persistence.
4. `pkg/config/config.go` (`LoadIPAMConfig`) — populate claim fields from CNI stdin
   sources when present. No behavior change when empty.
5. `pkg/allocate/allocate.go` (`AssignIPForClaim`) — reuse by claim ref / preferred
   IPs (idempotent take-over for stop/start and migration handoff) tag new
   reservations with `IPAMClaimRef`.
6. `pkg/api/...` + CRDs — dedicated `ipamclaimref` on `IPAllocation` and
   `OverlappingRangeIPReservationSpec` (does **not** overload `PodRef`).
7. `pkg/storage/kubernetes/ipam.go` — claim-aware Allocate / Deallocate:
   - Resolve claim, fetch `IPAMClaim`, reuse or allocate, persist status.
   - Skip freeing claim-backed reservations on Deallocate.
   - Overlapping-range create treats AlreadyExists as claim take-over (update
     PodRef / IfName / IPAMClaimRef).
8. `pkg/controlloop` — `ClaimController` watches IPAMClaim **delete** and frees
   matching pool / overlapping reservations. `garbageCollectPodIPs` skips any
   reservation with non-empty `IPAMClaimRef` (release is claim-delete only).
9. `pkg/reconciler` — orphan GC also skips claim-backed reservations.
10. Install path — IPAMClaim CRD + RBAC for `ipamclaims` / `ipamclaims/status` in
    daemonset, Helm chart, and kind e2e setup.
11. **Tests / docs** — unit tests (config, allocate, reference) e2e covers
    allocate → retain across pod recreate → release on claim delete; user docs
    (`doc/persistent-ips.md`) ship in #742. Live-migration e2e is not yet covered.

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

1. **Live migration overlap.** Source and target `virt-launcher` pods coexist and
  share the claim's IP for a window. Implementation allows take-over of an
  existing claim-backed reservation and preferred IPs from `status.ips`, and
  updates overlapping reservations on AlreadyExists. Concurrent ADD under heavy
  churn remains the main risk area e2e covers stop/start recreate, not full
  KubeVirt migration yet.
2. **Overlapping-range reservations** — addressed by tagging with `IPAMClaimRef`
  and updating (not failing) when the same IP is re-acquired for a new pod.
3. **Idempotency under churn** (old pod terminating while new pod starts) —
  handled via claim-ref matching and preferred-IP take-over in `AssignIPForClaim`.
4. **Stale reconciliation** — pod/reconciler GC never frees claim-backed rows
  only the IPAMClaim-delete controller does. If that controller was down across
  a claim delete, a restart + delete-event (or operator re-delete) is needed to
  free orphans broader resync cleanup is a follow-up if needed.

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
  resolves `ipam-claim-reference` from the pod network-selection annotation
  (same wiring as OVN-Kubernetes / ipam-extensions).
- **Live-migration handoff** — reuse claim-backed pool reservations and
  `status.ips` preferred addresses update `status.ownerPod` on ADD overlapping
  AlreadyExists is treated as take-over. Full migration e2e remains a follow-up.
- **Reservation tagging** — dedicated `IPAMClaimRef` / `ipamclaimref` field on
  IPPool allocations and overlapping reservations (does not overload `PodRef`).
- **Implementation** — [whereabouts#742](https://github.com/k8snetworkplumbingwg/whereabouts/pull/742).
