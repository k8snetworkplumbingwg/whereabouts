# Persistent IPs with IPAMClaim

Whereabouts can keep the same IP across pod recreation by anchoring the
allocation to an [`IPAMClaim`](https://github.com/k8snetworkplumbingwg/ipamclaims)
instead of the pod. This is the same contract OVN-Kubernetes uses for
KubeVirt VMs (via [`kubevirt/ipam-extensions`](https://github.com/kubevirt/ipam-extensions)).

When no claim is referenced, behavior is unchanged: IPs are allocated and
released with the pod lifecycle.

## When to use this

Use IPAMClaim-backed allocations when a workload must keep a stable address
across stop/start or live migration — typically KubeVirt VirtualMachines on a
Multus secondary network that uses Whereabouts IPAM.

## Prerequisites

1. Install the IPAMClaim CRD (shipped with Whereabouts for convenience):

```bash
kubectl apply -f doc/crds/k8s.cni.cncf.io_ipamclaims.yaml
```

2. Whereabouts DaemonSet RBAC must allow `ipamclaims` and `ipamclaims/status`
   (`get`/`list`/`watch`/`update`/`patch`). Current
   `doc/crds/daemonset-install.yaml` and the Helm chart include these rules.
3. The IP control-loop (part of the Whereabouts DaemonSet) must be running so
   claim deletions free pool reservations.

## How it works

1. A controller (for KubeVirt: `ipam-extensions`) creates an `IPAMClaim` owned by
   the VM and sets `ipam-claim-reference` on the pod's Multus network-selection
   element.
2. On CNI **ADD**, Whereabouts:
   - Resolves the claim (from CNI config / `args.cni`, or from the pod
     network-selection annotation)
   - Reuses `status.ips` when present, otherwise allocates and writes
     `status.ips` + `status.ownerPod`
   - Tags the IPPool (and overlapping-range) reservation with `ipamclaimref`
3. On CNI **DEL** / pod GC: claim-backed reservations are **kept**.
4. When the `IPAMClaim` is deleted, the claim controller releases the IPs.

## Manual example (without KubeVirt)

Create a claim and a Multus-annotated pod that references it:

```yaml
apiVersion: k8s.cni.cncf.io/v1alpha1
kind: IPAMClaim
metadata:
  name: demo-claim
  namespace: default
spec:
  network: wa-nad
  interface: net1
---
apiVersion: v1
kind: Pod
metadata:
  name: demo-pod
  namespace: default
  annotations:
    k8s.v1.cni.cncf.io/networks: |
      [{
        "name": "wa-nad",
        "interface": "net1",
        "ipam-claim-reference": "demo-claim"
      }]
spec:
  containers:
  - name: pause
    image: registry.k8s.io/pause:3.9
```

After the pod is Ready:

- `IPAMClaim.status.ips` lists the allocated address(es)
- The matching `IPPool` allocation includes `ipamclaimref: default/demo-claim`

Delete the pod and recreate another with the same `ipam-claim-reference`: the
new pod receives the same IP. Delete the `IPAMClaim` to return the address to
the pool.

## KubeVirt

For VirtualMachines, install
[`kubevirt/ipam-extensions`](https://github.com/kubevirt/ipam-extensions). It
creates `IPAMClaim` objects and injects `ipam-claim-reference` into the
launcher pod's Multus annotation. Whereabouts then provides persistent IPs on
non-OVN-Kubernetes clusters without KubeVirt API changes.

## Limitations

- Live migration briefly has source and target pods sharing the claim IP;
  `status.ownerPod` tracks the handoff. Concurrent ADD under heavy churn is the
  main risk area.
- Creating/deleting `IPAMClaim` resources is out of scope for Whereabouts; that
  remains the job of `ipam-extensions` (or your own controller).
- Persistent IPs for arbitrary non-claim-backed pods are not a goal of this
  feature (the mechanism is generic if you create claims yourself).
