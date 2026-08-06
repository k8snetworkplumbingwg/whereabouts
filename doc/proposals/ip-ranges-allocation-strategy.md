# IP ranges allocation strategy

Status: Draft

# Table of contents

- [Introduction](#introduction)
  - [Goals](#goals)
  - [Non-goals](#non-goals)
- [Design](#design)
  - [Configuration](#configuration)
  - [Range ordering](#range-ordering)
  - [Allocation](#allocation)
  - [Idempotent retries](#idempotent-retries)
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
- Define ordering, exhaustion, retries, deallocation, overlapping ranges, and
  mixed-family behavior precisely.
- Use an API that can be extended with additional allocation strategies later.

### Non-goals

- Allocating CIDR network or broadcast endpoint addresses.
- Refactoring IP range helpers or introducing a new pool abstraction.
- Changing IP selection within a range; Whereabouts continues to choose the
  lowest available address according to the existing range configuration.
- Changing the existing standard or Fast IPAM lease selection and datastore
  concurrency models.
- Implementing the feature in this proposal change. Implementation will follow
  after maintainers agree on the API.

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

Reordering ranges changes allocation preference but does not move existing
allocations between pools.

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
   reference and interface, as described in [Idempotent retries](#idempotent-retries).
2. If no existing allocation is found, visit ranges in normalized order.
3. Attempt allocation from the current range using the existing within-range
   address selection and overlapping-range checks.
4. If the range reports typed exhaustion, continue to the next range.
5. On the first successful allocation, persist it, return that one address,
   and stop visiting ranges.
6. If every range is exhausted, return an exhaustion error and no address.

Fallback is only allowed for an error that specifically means the current
range has no allocatable address. Parse, validation, API, authorization,
timeout, conflict-after-retries, and other operational errors abort the
request. Treating arbitrary failures as exhaustion could hide a broken pool
and allocate from a lower-priority range unexpectedly.

### Idempotent retries

CNI ADD can be retried after the first attempt has persisted an allocation but
before its result reaches the caller. A retry must return the original
allocation instead of creating another one.

Before attempting any new allocation, `first_available` searches the pool
record for every currently configured range for a reservation whose `podRef`
and interface name match the request. The search covers every configured range
even if an earlier range currently has capacity. This is necessary for the
following sequence:

1. The first range is exhausted, so a Pod receives an address from the second
   range.
2. Capacity later becomes available in the first range.
3. The Pod's CNI ADD is retried.

Without a complete preflight search, step 3 would allocate a second address
from the first range. With the search, Whereabouts returns the address already
held in the second range and refreshes its container ID if needed, consistent
with existing retry behavior within one pool.

Failure to inspect any configured pool is an operational error; allocation
must not proceed on incomplete knowledge. If inconsistent state contains more
than one matching reservation, Whereabouts retryably refreshes the container
ID on every match before returning the first match in configured order. If any
refresh fails, ADD returns an operational error; a subsequent retry repeats the
complete search and must not allocate another address. The inconsistency is
reported in logs.

When overlap protection is enabled, each unique preflight match must also pass
through the existing overlapping-range reservation verification and update
path before ADD succeeds. This repairs the failure window in which the IPPool
allocation was persisted but its `OverlappingRangeIPReservation` was not. A
missing reservation is recreated for the matching Pod and interface; a
reservation owned by another workload is an inconsistency error. API or update
failures are operational errors, and the existing IPPool allocation is not
returned until overlap protection has been reconciled.

### Deallocation

Deallocation visits every configured pool in normalized order. Not finding the
allocation in an earlier pool does not stop the search. Every reservation that
matches the existing container-ID-and-interface deletion identity is removed,
together with its overlapping-range reservation when enabled. Refreshing every
duplicate match during ADD ensures a later DEL using the current container ID
can remove all of them.

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
- A retry that finds an existing IPPool allocation verifies or restores its
  corresponding overlapping-range reservation before returning the address.
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
Exhaustion must be represented by a typed error so the range loop can
distinguish expected capacity fallback from operational failure. If all ranges
are exhausted, the returned error should identify total exhaustion and retain
the per-range context needed for diagnostics.

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

Changing the strategy or reordering ranges affects new allocations only;
Whereabouts does not migrate existing addresses between pools. If a workload
already has matching reservations in more than one pool (for example, after
changing from `all` to `first_available`), `first_available` returns the first
match in configured order after refreshing every match's container ID and does
not create another reservation. A later CNI DEL searches all configured pools
as described above.

Removing or replacing a range changes the identity of the pool that allocation
and deallocation inspect. Whereabouts cannot discover an allocation in a pool
that is no longer represented in the configuration. Operators must therefore
append replacement capacity while retaining old ranges until their IPPools
have drained; only then may the old entries be removed. Removing or replacing
a range that still has live allocations is unsupported and can otherwise
produce a duplicate allocation on retry or an orphan on deletion.

<hr>

## Implementation plan

After the API is accepted:

1. Add the strategy type and `ipRangesAllocation` field to configuration
   parsing, default omission to `all`, and reject unsupported values.
2. Refactor the Kubernetes allocation path so `first_available` first searches
   each unique resolved pool for an existing Pod/interface allocation, then
   uses ordered fallback only for typed exhaustion errors and returns after one
   successful allocation.
3. When preflight finds multiple matches, retryably refresh every matching
   container ID before returning the first configured match.
4. Reconcile each unique matching overlapping-range reservation before a
   preflight match can make ADD succeed.
5. Make deallocation search every pool and clean up every matching reservation.
6. Preserve existing mode-specific lease selection, datastore retries, overlap
   handling, and the default multi-address path. Reject `first_available` with
   `node_slice_size` until Fast IPAM multiple-range semantics are designed.
7. Document the user-facing option and safe range-draining procedure in the
   extended configuration guide.

## Test plan

Unit and end-to-end coverage will include:

- omission and explicit `all` preserving one address per range;
- allocation from the first range with capacity;
- fallback after typed exhaustion of an earlier range;
- total exhaustion across all ranges;
- rejection of empty, unknown, and incorrectly cased strategies;
- retry returning an existing later-pool allocation after an earlier pool
  regains capacity;
- retry restoring overlap protection after IPPool persistence succeeds but
  overlapping-range reservation persistence fails;
- retryably refreshing duplicate matches with different container IDs, then
  deleting all of them;
- deletion of an allocation from a later pool;
- overlapping ranges with overlap protection enabled and disabled;
- identical entries resolving to one pool traversal while preserving the first
  configured occurrence;
- mixed IPv4/IPv6 ranges producing one address total with `first_available`;
- legacy `range` alone and combined with `ipRanges`;
- appending and reordering ranges while an existing allocation remains
  discoverable, followed by removal only after the old pool drains;
- rejection of `first_available` combined with `node_slice_size`.

The implementation change will run `make test`. The documented kind-based E2E
suite will also run when locally feasible; otherwise the implementation PR will
report that it relies on the upstream E2E workflow.

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
- Implementation is intentionally deferred until the API is accepted.
