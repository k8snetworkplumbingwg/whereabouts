# Persistent IPs for KubeVirt VMs via the IPAMClaim standard

Status: **Draft / for discussion** (targets whereabouts#557, whereabouts#500)

This proposal describes how Whereabouts can implement the multi-network de-facto
standard `IPAMClaim` contract so that KubeVirt VirtualMachines keep a stable IP
across stop/start and live migration — the same capability OVN-Kubernetes already
provides through `[kubevirt/ipam-extensions](https://github.com/kubevirt/ipam-extensions)`
and the `[ipamclaims](https://github.com/k8snetworkplumbingwg/ipamclaims)` CRD — **without**
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
- [Hard problems / open questions](#hard-problems--open-questions)
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
pod. OVN-Kubernetes already honors these claims Whereabouts does not yet.

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
- Persistent IPs for arbitrary (non-KubeVirt) pods, though the mechanism is generic.
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
`k8s.v1.cni.cncf.io/networks` network-selection element for the eligible interface,
and Multus forwards network-selection-element attributes to the delegate plugin.

> **Open item to confirm before PR1:** the exact transport by which the reference
> reaches the delegate IPAM plugin — a top-level field injected into the delegate
> CNI config, `RuntimeConfig`, or `CNI_ARGS`. We will mirror whatever OVN-Kubernetes
> reads (it is the reference implementation) so Whereabouts is drop-in compatible
> with the same `ipam-extensions`/Multus wiring.



### Changes in Modules

1. `go.mod` **/ vendor** — add `github.com/k8snetworkplumbingwg/ipamclaims`
  (API types + generated clientset).
2. `pkg/types/types.go` — extend `IPAMConfig`:
  ```go
   IPAMClaimReference string // name of the IPAMClaim, empty => legacy behavior
   IPAMClaimNamespace string // usually the pod namespace
  ```
3. `pkg/config/config.go` **(**`LoadIPAMConfig`**)** — populate the fields above from
  the transport confirmed in the open item. No behavior change when empty.
4. `pkg/storage/kubernetes/ipam.go` **(**`IPManagement`**,** `Allocate` **branch)** —
  claim-aware allocation:
  - Fetch the `IPAMClaim`.
  - `len(status.ips) > 0` → reuse those IPs reserve them **idempotently** (a repeat
  reservation for the same claim is a success, not a conflict).
  - else → allocate as today, then patch `status.ips` + `status.ownerPod`.
  - Tag the pool/overlapping reservation so it is recognizable as claim-backed
  (e.g. store the claim ref alongside `PodRef`).
5. `pkg/storage/kubernetes/ipam.go` **(**`Deallocate` **branch)** — if the reservation
  is claim-backed, skip freeing the address (optionally clear `ownerPod`) keep the
   reservation intact so the next pod re-acquires the same IP.
6. `pkg/controlloop` — release on claim deletion, not pod deletion:
  - Add an `IPAMClaim` informer on **delete**, free the claim's reserved IPs via
   the existing `Deallocate` cleanup path.
  - In `garbageCollectPodIPs`, skip GC for a claim-backed allocation whose claim
  still exists (a generalization of the current StatefulSet guard).
7. **Tests** — unit tests for config parsing, reuse-vs-allocate, and DEL-no-release
  an `e2e/` scenario exercising VM stop/start and (ideally) migration.



### Lifecycle walk-through


| Event                  | Actor                         | Result                                                                                                       |
| ---------------------- | ----------------------------- | ------------------------------------------------------------------------------------------------------------ |
| VM created             | ipam-extensions               | Creates `IPAMClaim` (empty `status.ips`), owned by the VM injects `ipam-claim-reference` on the launcher pod |
| Pod ADD (first boot)   | Whereabouts                   | `status.ips` empty → allocate, write `status.ips`, set `ownerPod`                                            |
| VM stop → pod DEL      | Whereabouts                   | Claim-backed → **keep** the IP control-loop **skips** GC because the claim still exists                      |
| VM start → new pod ADD | Whereabouts                   | `status.ips` populated → **reuse** the same IP                                                               |
| Live migration         | Whereabouts                   | Target pod ADD reuses the claim IP while source still holds it `ownerPod` hands off                          |
| VM deleted             | ipam-extensions → Whereabouts | `IPAMClaim` deleted → control-loop **frees** the IP                                                          |




## Hard problems / open questions

1. **Live migration overlap.** Source and target `virt-launcher` pods coexist and
  share the claim's IP for a window. The target must be allowed to reuse the IP
   while the source reservation still exists `status.ownerPod` mediates the handoff.
   This is the primary concurrency risk and needs explicit review.
2. **Overlapping-range reservations** are pod-keyed today re-acquiring the same IP
  for a new pod must not be treated as a duplicate/conflict.
3. **Idempotency under churn** (old pod terminating while new pod starts).
4. **Stale reconciliation** — if the control-loop was down when a claim was deleted,
  the informer resync must free orphaned reservations.
5. **Reference transport** — the one item to confirm against OVN-Kubernetes/Multus
  (see above) before locking the config parsing.



## Delivery plan


| PR  | Scope                                                                         | Risk   |
| --- | ----------------------------------------------------------------------------- | ------ |
| PR0 | This design note align at a community meeting                                 | none   |
| PR1 | Vendor `ipamclaims` add `IPAMClaimReference` + config parsing (inert)         | low    |
| PR2 | Allocate path: reuse existing / persist new `status.ips`                      | medium |
| PR3 | Deallocate path: do not release claim-backed IPs on pod DEL                   | medium |
| PR4 | Control-loop: IPAMClaim-delete watcher + skip GC for claim-backed allocations | medium |
| PR5 | e2e (stop/start + migration) and docs                                         | low    |




## Summary

Anchor persistent allocations to the `IPAMClaim` (pod-independent, VM-owned) instead
of the pod. On ADD, reuse `status.ips` when present else allocate and persist them
on pod DEL, keep claim-backed IPs release only when the claim is deleted. This makes
Whereabouts interoperable with the existing `ipam-extensions`/`IPAMClaim` machinery,
delivering persistent VM IPs on non-OVN-Kubernetes clusters with no KubeVirt API
change and full backwards compatibility.

## Discussions and Decisions

- *TBD* — reference-transport mechanism confirmed against OVN-Kubernetes.
- *TBD* — live-migration handoff semantics (`ownerPod`) agreed with maintainers.
- *TBD* — whether reservation records gain a dedicated claim field or overload `PodRef`.

