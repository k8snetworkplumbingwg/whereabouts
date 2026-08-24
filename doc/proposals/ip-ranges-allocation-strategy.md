# IP ranges allocation strategy

Status: Draft

# Table of contents

- [Introduction](#introduction)
  - [Goals](#goals)
  - [Use cases](#use-cases)
  - [Non-goals](#non-goals)
- [Design](#design)
  - [Configuration](#configuration)
  - [Range ordering](#range-ordering)
  - [Allocation](#allocation)
  - [Existing allocation reuse](#existing-allocation-reuse)
  - [Deallocation](#deallocation)
  - [Overlapping ranges](#overlapping-ranges)
  - [Mixed address families](#mixed-address-families)
  - [Errors and concurrency](#errors-and-concurrency)
  - [Backward compatibility](#backward-compatibility)
  - [Changing strategies and ranges](#changing-strategies-and-ranges)
- [Implementation plan](#implementation-plan)
- [Test plan](#test-plan)
- [Alternatives](#alternatives)
- [Acknowledgements](#acknowledgements)
- [Discussions and decisions](#discussions-and-decisions)

<hr>

## Introduction

Whereabouts currently allocates one address from every entry in `ipRanges`.
This is useful for dual-stack and multi-address attachments, but it cannot model
one logical pool assembled from several ordered, disjoint ranges. In that use
case, exhausting the first range should cause allocation to continue in the
next range while the workload still receives exactly one address.

This proposal adds an extensible allocation strategy to select between the
current behavior and ordered, single-address fallback. It deliberately
separates that API decision from the implementation.

### Goals

- Preserve the current one-address-per-range behavior by default.
- Allow a configuration to allocate one address total from the first range
  with capacity.
- Define ordering, exhaustion, existing allocation reuse, deallocation,
  overlapping ranges, and mixed-family behavior precisely.
- Use an API that can be extended with additional allocation strategies later.

### Use cases

The primary use case is incremental expansion with disjoint address blocks. An
operator might initially receive `10.1.2.8/29` from a larger `10.1.0.0/16`
network. When that block is exhausted, the operator might receive
`10.1.3.8/29`, which is not a contiguous extension of the original block. In a
`first_available` configuration, the new block is appended to `ipRanges`;
existing workloads keep their addresses, and new workloads receive one address
from the first range with capacity. This is the use case described in
[PR #649](https://github.com/k8snetworkplumbingwg/whereabouts/pull/649#issuecomment-3748492842).

The same strategy also covers a logical single-address pool assembled from
several ordered, disjoint ranges when the network is first configured. In both
cases, every workload receives one address total and range order provides
deterministic fallback.

These use cases require creating a range sequence and, for `first_available`,
appending capacity. They do not require changing the allocation strategy or
mutating existing entries while they have live allocations. Reordering,
editing, replacing, or removing existing ranges is unsupported until the
network is drained. Dual-stack and multi-address attachments are separate use
cases and retain the `all` strategy.

### Non-goals

- Allocating CIDR network or broadcast endpoint addresses.
- Refactoring IP range helpers or introducing a new pool abstraction.
- Changing IP selection within a range; Whereabouts continues to choose the
  lowest available address according to the existing range configuration.
- Changing the existing standard or Fast IPAM lease selection and datastore
  concurrency models.
- Persisting configuration generations or introducing a new CRD to enforce
  configuration-update admission.
- This proposal defines the allocation-strategy API only. Implementation will
  follow after maintainers agree on it.

<hr>

## Design

### Configuration

Add the optional `ipRangesAllocation` string to the IPAM configuration. The
initial supported values are:

| Value | Behavior |
| --- | --- |
| omitted | Equivalent to `all`. |
| `all` | Allocate one address from every configured range. This is the current behavior. |
| `first_available` | Allocate exactly one address total from the first range with capacity. |

Values are case-sensitive. An explicitly configured empty string or any value
other than those listed above is a configuration error.

Parsing retains whether the field was present until the inline and flat-file
configuration sources have been validated and merged. Each source is validated
before merging, so an explicit empty or unsupported value is rejected rather
than replaced by a value from the other source. A valid inline value takes
precedence; the flat-file value is used only when the inline configuration
omits the field. If both sources omit it, the result is normalized to `all`.
After normalization, the runtime does not distinguish an explicit `all` from
the default.

For example, the following configuration treats two disjoint CIDRs as ordered
capacity for a single attachment:

```json
{
  "cniVersion": "0.3.1",
  "name": "ordered-ranges",
  "ipam": {
    "type": "whereabouts",
    "ipRanges": [
      {
        "range": "192.168.10.0/24"
      },
      {
        "range": "192.168.20.0/24"
      }
    ],
    "ipRangesAllocation": "first_available"
  }
}
```

The field describes how the `ipRanges` collection is consumed rather than a
property of any individual range. A string strategy is preferred over a
boolean such as `singleIP` so future policies can be added without introducing
another set of interacting flags.

### Range ordering

The normalized `IPRanges` order is authoritative. Whereabouts already converts
the legacy top-level `range` fields into a `RangeConfiguration` and prepends it
to `IPRanges`. Therefore, when both forms are present, `first_available` tries
the legacy `range` first and then the explicit `ipRanges` entries in their JSON
order.

The configured order must remain stable while the network has live
allocations. For `first_available`, additional capacity may be appended through
entries that resolve to new IPPool identifiers, but existing entries must not
be reordered or modified. The operational contract is specified in
[Changing strategies and ranges](#changing-strategies-and-ranges).

For `first_available`, Whereabouts resolves entries to their existing IPPool
identifiers and visits each unique pool only once, at the position of its first
configured occurrence. Preflight, fallback, duplicate refresh, and deallocation
all use this de-duplicated traversal. Repeating an identical range with the same
`network_name` therefore does not create an independent source of capacity.

### Allocation

The `all` strategy retains the existing allocation loop and its behavior.

The `first_available` strategy performs these steps while holding the lease
selected by the existing IPAM mode:

1. Search every configured pool for an existing allocation for the same Pod
   reference and interface, as described in
   [Existing allocation reuse](#existing-allocation-reuse).
2. If no existing allocation is found, visit ranges in normalized order.
3. Attempt allocation from the current range using the existing within-range
   address selection and overlapping-range checks.
4. If allocation returns `allocate.AssignmentError`, detected with `errors.As`,
   record that range's exhaustion context and continue to the next range.
5. On the first successful allocation, persist it, return that one address,
   and stop visiting ranges.
6. If every unique pool is exhausted, return `AllRangesExhaustedError` and no
   address.

Fallback is only allowed for an error that specifically means the current
range has no allocatable address. Parse, validation, API, authorization,
timeout, conflict-after-retries, and other operational errors abort the
request. Treating arbitrary failures as exhaustion could hide a broken pool
and allocate from a lower-priority range unexpectedly.

### Existing allocation reuse

Whereabouts is a delegated IPAM plugin. It does not create or configure the
interface named by `CNI_IFNAME`. The upper CNI plugin remains responsible for
the [CNI ADD requirements](https://www.cni.dev/docs/spec/#add-add-container-to-network-or-apply-modifications),
including returning an error if the requested interface already exists in the
target sandbox. This proposal does not make it valid for a runtime to invoke
ADD twice without an intervening DEL for the same
`(CNI_CONTAINERID, CNI_IFNAME)` tuple.

Whereabouts already uses the Pod reference and interface name to find an
existing allocation within one pool. When the upper CNI plugin configures a
replacement sandbox for the same logical attachment, `CNI_CONTAINERID` and
`CNI_NETNS` differ while the Pod reference and `CNI_IFNAME` remain the same.
The requested interface does not already exist in the new sandbox. In this
case, delegated Whereabouts ADD preserves the existing behavior: it returns the
existing allocation and refreshes its stored container ID instead of allocating
another address. This case is not specific to StatefulSets.

Before attempting any new allocation, `first_available` searches the pool
record for every currently configured range for a reservation whose `podRef`
and interface name match the request. The search covers every configured range
even if an earlier range currently has capacity. This is necessary for the
following sequence:

1. The first range is exhausted, so a Pod receives an address from the second
   range.
2. Capacity later becomes available in the first range.
3. The upper CNI plugin configures a replacement sandbox and invokes delegated
   Whereabouts ADD with a different `CNI_CONTAINERID` for the same Pod reference
   and interface name.

Without a complete preflight search, step 3 would allocate a second address
from the first range. With the search, Whereabouts returns the address already
held in the second range and refreshes its container ID if needed, preserving
the existing behavior within one pool.

Failure to inspect any configured pool is an operational error; allocation
must not proceed on incomplete knowledge. The complete search records every
match before performing any updates. More than one match is inconsistent and
is reported in logs.

When overlap protection is enabled, a validation pass reads the corresponding
`OverlappingRangeIPReservation` for every unique preflight match before any
repair or container-ID refresh. This is a narrow repair of the existing
two-record update, not general reconciliation, and it does not rebuild network
state from Pods, network-status annotations, or the container runtime. If any
overlapping-range reservation exists for a different Pod or interface,
Whereabouts does not overwrite it or choose an authoritative owner; ADD leaves
the conflicting records unchanged and returns an operational inconsistency
error. Otherwise, a missing reservation is recreated from the matching IPPool
reservation and the current ADD request before the address is returned. API
failures are also operational errors. Broader recovery from conflicting or
orphaned state belongs in a reconciler and is outside this proposal.

After overlap validation and repair, Whereabouts uses the existing datastore
retry handling to refresh the container ID on every preflight match before
returning the first match in configured order. If any refresh fails, ADD
returns an operational error without allocating another address. Every valid
delegated ADD performs the complete search before any allocation attempt.

### Deallocation

Deallocation visits every configured pool in normalized order. Not finding the
allocation in an earlier pool does not stop the search. Every reservation that
matches the existing container-ID-and-interface deletion identity is removed,
together with its overlapping-range reservation when enabled. Refreshing every
duplicate match during existing allocation reuse ensures a later DEL using the
current container ID can remove all of them.

Searching all pools makes deletion deterministic when the address came from a
later fallback range. As today, deleting an allocation that is already absent
succeeds.

### Overlapping ranges

The existing `enable_overlapping_ranges` behavior remains in force and remains
scoped by `network_name`:

- When overlap protection is enabled, an address reserved by another Pod is
  unavailable even if it appears in another configured range. Allocation
  continues within the current range, then falls back only if that range is
  exhausted.
- An ADD that finds an existing IPPool allocation verifies its corresponding
  overlapping-range reservation and recreates it only when it is absent. A
  reservation with a conflicting Pod or interface is left unchanged and causes
  an operational error.
- When overlap protection is disabled, `first_available` does not add a new
  cross-range uniqueness guarantee; the current collision behavior is
  preserved.

No composite IPPool is introduced for overlapping entries. Their configured
order remains the fallback order, and the existing range-to-IPPool mapping
remains the unit of persistence. Identical entries that resolve to the same
IPPool are visited once as described in [Range ordering](#range-ordering).

### Mixed address families

`first_available` means one address total, not one address per family. Given an
IPv4 range followed by an IPv6 range, a request receives an IPv4 address while
the first range has capacity and falls back to one IPv6 address only after the
IPv4 range is exhausted.

Dual-stack configurations that require both an IPv4 and an IPv6 address must
omit `ipRangesAllocation` or set it to `all`.

### Errors and concurrency

Configuration parsing validates the strategy before any datastore operations.
The existing `allocate.AssignmentError` is the sole per-range exhaustion signal.
The allocator returns it only after scanning a valid range without finding an
allocatable address. The range loop uses `errors.As` so a layer may add context
with `%w` without losing the typed signal. Parse, validation, overlap, API,
authorization, timeout, datastore, and conflict-after-retries errors do not wrap
an `AssignmentError`; they propagate unchanged and abort fallback.

If every unique pool returns `AssignmentError`, the range loop returns a new
`AllRangesExhaustedError`. Its `Ranges []RangeExhaustion` field is ordered by
traversal. Each entry contains `Index int` for the pool's first normalized
configured occurrence, `Pool PoolIdentifier` for the resolved pool, and
`Err allocate.AssignmentError` for that attempt. Its `Unwrap() []error` returns
the `Err` values in the same order, preserving their diagnostic details. The
aggregate type distinguishes total exhaustion from the per-range signal used
internally for fallback.

Standard IPAM continues to run the entire preflight search and
allocation/deallocation operation under the current cluster-wide Whereabouts
lease. Fast IPAM currently selects a node/pool-specific lease and does not yet
support multiple ranges. Configuration validation therefore rejects combining
`first_available` with `node_slice_size`. Supporting that combination requires
a separate design for range ordering and cross-pool serialization.

This proposal does not otherwise change lease selection. Existing datastore
retry handling also remains in place; exhaustion is considered only after the
current pool's transient retries are resolved.

### Backward compatibility

- Existing configurations omit `ipRangesAllocation` and therefore keep `all`.
- Explicit `ipRangesAllocation: "all"` has the same behavior as omission.
- When `node_slice_size` is not configured, configurations containing only the
  legacy top-level `range` behave the same under either strategy because there
  is only one normalized range.
- When both `range` and `ipRanges` are configured, the legacy range remains
  first, preserving the existing normalization order.
- No IPPool or overlapping-range CRD schema changes or migrations are required.

### Changing strategies and ranges

The normalized allocation strategy, `network_name`, and existing normalized
range sequence, including every value used to derive IPPool identifiers, are
immutable while the network has live allocations. This means operators must
not change between `all` and `first_available`, rename the network, reorder
ranges, edit an existing range, or remove or replace one. Such changes can alter
the result when reusing an allocation for a replacement sandbox, make an
existing reservation undiscoverable, or orphan it during DEL.

For `first_available`, the only supported live update is appending one or more
ranges. Existing entries and their order form an immutable prefix of the
updated configuration. The first appended entry for each IPPool identifier not
already present in the de-duplicated traversal extends capacity for future
allocations; it does not move existing allocations. An appended entry that is
identical to an existing range and resolves to the same IPPool identifier is a
duplicate and remains a no-op: it does not add independent capacity or alter
the traversal. The complete preflight search still returns a workload's
existing allocation before attempting to allocate from any range.

Configurations using `all` must be drained before appending a range as well as
before any other range change. Under the existing `all` allocation loop, an ADD
for a replacement sandbox after an append would allocate an additional address
from the new range and change the result for an existing workload.

Old ranges remain configured until the entire network is drained. After all
allocations have been released, operators may change the strategy or
`network_name`, or edit, replace, reorder, or remove ranges before creating new
allocations.

Whereabouts reads this configuration from CNI configuration, commonly embedded
in a NetworkAttachmentDefinition, rather than from a dedicated CRD with update
admission. Without persisting a configuration generation with reservations, it
cannot compare the current configuration with a previous version and enforce
this invariant. This proposal therefore defines the supported operational
contract: configuration management must prevent unsupported live changes.

<hr>

## Implementation plan

After the API is accepted:

1. Add the strategy type and a presence-aware `ipRangesAllocation` parser
   representation. Validate each source before flat-file merging, apply the
   precedence rules above, then normalize omission to `all`.
2. Refactor the Kubernetes allocation path so `first_available` first searches
   each unique resolved pool for an existing Pod/interface allocation, then
   uses `errors.As` to fall back only on `allocate.AssignmentError`, returns
   after one successful allocation, and returns an ordered
   `AllRangesExhaustedError` when every unique pool is exhausted.
3. When overlap protection is enabled, verify all corresponding
   overlapping-range reservations before performing any preflight update.
   Recreate missing secondary records only after the validation pass finds no
   ownership conflict; fail without modifying state when ownership conflicts.
4. Use the existing datastore retry handling to refresh every matching
   container ID, then return the first match in configured order.
5. Make deallocation search every pool and clean up every matching reservation.
6. Preserve existing mode-specific lease selection, datastore retries, overlap
   handling, and the default multi-address path. Reject `first_available` with
   `node_slice_size` until Fast IPAM multiple-range semantics are designed.
7. Document the user-facing option, append-only `first_available` capacity
   expansion through new unique IPPools, duplicate-append no-op semantics, and
   the drain-before-reconfiguration contract in the extended configuration
   guide.

## Test plan

Unit coverage will include:

- omission and explicit `all` preserving one address per range;
- allocation from the first range with capacity;
- fallback when an earlier range returns a direct or contextually wrapped
  `allocate.AssignmentError`;
- an operational error in an earlier range being returned with the same error
  identity and aborting allocation without visiting a later range;
- total exhaustion returning `AllRangesExhaustedError` with ordered configured
  indices, resolved pool identifiers, and underlying assignment errors, with
  `Unwrap()` returning those exact errors in traversal order and `errors.As`
  reaching the first underlying `allocate.AssignmentError`;
- rejection of empty, unknown, and incorrectly cased strategies;
- inline and flat-file precedence, including rejection of invalid values in
  either source and normalization of omission or explicit `all`;
- replacement-sandbox ADD returning an existing later-pool allocation after an
  earlier pool regains capacity;
- ADD finding an existing allocation restoring overlap protection after IPPool
  persistence succeeds but overlapping-range reservation persistence fails;
- ADD finding an existing allocation rejecting an overlapping-range
  reservation owned by a different Pod or interface without overwriting either
  record;
- multiple preflight matches where an earlier overlap reservation is missing
  and a later one has conflicting ownership, returning an operational error
  without recreating the missing record or refreshing any container ID;
- refreshing duplicate matches with different container IDs using the existing
  datastore retry handling, then deleting all of them;
- deletion of an allocation from a later pool;
- overlapping ranges with overlap protection enabled and disabled;
- identical entries resolving to one pool traversal while preserving the first
  configured occurrence;
- mixed IPv4/IPv6 ranges producing one address total with `first_available`;
- legacy `range` alone and combined with `ipRanges`;
- appending a range that resolves to a new IPPool under `first_available` while
  an existing allocation remains discoverable, then allocating new workloads
  from the appended range after earlier exhaustion;
- appending a duplicate range under `first_available` remaining a no-op without
  adding capacity or altering the de-duplicated traversal;
- changing the strategy or `network_name`, or editing, replacing, reordering,
  or removing ranges after the network has been fully drained;
- appending a range under `all` only after the network has been fully drained;
- rejection of `first_available` combined with `node_slice_size`.

The standard kind-based E2E suite will cover the observable behavior of:

- omission and explicit `all` continuing to allocate one address per range;
- `first_available` allocating from the first range with capacity;
- fallback to a later range after earlier exhaustion;
- total exhaustion after all configured ranges are full;
- appending a new unique range while earlier allocations remain, then using it
  for a new workload after the earlier ranges are exhausted;
- deleting and reusing an allocation from a later range; and
- mixed-family `first_available` returning one address total.

`first_available` rejects `node_slice_size`, so no feature-specific node-slice
E2E case is required. The existing node-slice suite remains a regression check.

The implementation PR will run `make test` and add both unit and E2E coverage.
The documented kind-based E2E suite will also run locally when the required
environment is available. Regardless of local execution, the upstream standard
E2E job is expected to pass. Any waiver of new E2E coverage must be justified
in the implementation PR and agreed by maintainers.

## Alternatives

### A `singleIP` boolean

This is the smallest configuration change and was prototyped in the original
implementation. It describes only the result count, however, and cannot name
the selection policy. Supporting future policies such as balanced or random
selection would require additional flags with unclear interactions. An enum is
more explicit and extensible.

### Always allocate from the first non-exhausted range

Changing the default would break existing dual-stack and multi-address
configurations. The new behavior must be opt-in.

### Merge ranges into one persisted pool

A composite pool could hide fallback from the allocation loop, but it would
change the existing one-CIDR-per-IPPool persistence model and require migration
and new range-membership semantics. Keeping separate pools makes the API
change independent of storage schema changes.

## Acknowledgements

This proposal extracts the single-address-across-ranges use case from
[PR #649](https://github.com/k8snetworkplumbingwg/whereabouts/pull/649), created
by [Dmitrii Gadeev (@kruftik)](https://github.com/kruftik). That contribution
demonstrated the use case and provided an initial implementation and tests.

PR #649 also proposes allocation of CIDR network and broadcast endpoint
addresses and related pool/IP-helper refactoring. Those changes are
deliberately excluded here so the allocation-strategy API can be evaluated and
implemented independently.

## Discussions and decisions

- Proposed initial strategies: `all` and `first_available`.
- Proposed default: `all` for backward compatibility.
- Proposed API shape: an extensible string strategy instead of a boolean.
- Proposed live-update contract: `first_available` permits append-only capacity
  expansion through new unique IPPools, while duplicate appends remain no-ops;
  appending or otherwise changing ranges under `all`, and every other strategy,
  pool-identity, or range-sequence change, require a fully drained network.
- Configuration immutability is an operational invariant because this proposal
  does not add persisted configuration generations or update admission.
- Implementation is intentionally deferred until the API is accepted.
