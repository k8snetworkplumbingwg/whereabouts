# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Whereabouts is an IP Address Management (IPAM) CNI plugin for Kubernetes that assigns IP addresses cluster-wide. Unlike host-local which only works per-node, Whereabouts provides cluster-wide IP assignment by tracking allocations in either Kubernetes Custom Resources (CRDs) or etcd.

The plugin assigns IPs from a specified range (CIDR notation), always allocating the lowest available address. It supports both IPv4 and IPv6, and is commonly used with Multus CNI for multi-network configurations.

## Common Development Commands

### Building
```bash
# Build the CNI plugin binary
./hack/build-go.sh

# Build Docker image
make docker-build
# or with custom registry/tag:
IMAGE_REGISTRY=myregistry IMAGE_TAG=v1.0 make docker-build
```

### Testing
```bash
# Run unit tests (builds, installs tools, runs tests with coverage)
make test

# Run unit tests without static check (faster for iteration)
make test-skip-static

# Run unit tests for a single package
go test -v ./pkg/allocate/

# Run unit tests without cache (force re-run)
go test -count=1 -v ./pkg/storage/

# Run e2e tests locally
# 1. Install godotenv: go install github.com/joho/godotenv/cmd/godotenv@latest
# 2. Create kind cluster: make kind
# 3. Create e2e/.env with: KUBECONFIG=$HOME/.kube/config
# 4. Run tests from e2e directory:
cd e2e && godotenv -f .env go test -v . -timeout=1h
```

### Code Generation
```bash
# Verify generated code is up to date
./hack/verify-codegen.sh

# Update generated code (clientsets, informers, listers)
./hack/update-codegen.sh

# Regenerate API code and clean up artifacts
make generate-api
```

### Dependencies
```bash
# Update Go dependencies
make update-deps
# This runs: go mod tidy && go mod vendor && go mod verify
```

### Local Development Cluster
```bash
# Create a kind cluster with whereabouts installed (default: 2 worker nodes)
make kind

# Create with specific number of worker nodes
make kind COMPUTE_NODES=3
```

## Architecture

### Core Components

**CNI Plugin Entry Point** (`cmd/whereabouts.go`)
- Implements the CNI specification interface (ADD, DEL, CHECK, VERSION commands)
- `cmdAddFunc`: Allocates an IP address when a pod interface is created
- `cmdDelFunc`: Releases an IP address when a pod interface is deleted
- Loads IPAM configuration from stdin and creates a Kubernetes IPAM client

**IP Allocation Logic** (`pkg/allocate/`)
- `AssignIP`: Main allocation function that assigns IPs from a range
- Iterates through the IP range to find the lowest available IP
- Checks for existing allocations by podRef and ifName (idempotent behavior)
- Respects exclude ranges and avoids IPs ending in `.0`

**Storage Backend** (`pkg/storage/`)
- Abstract interface for IP pool management (currently Kubernetes CRDs primary, etcd legacy)
- `Store` interface: GetIPPool, GetOverlappingRangeStore, Status, Close
- `IPPool` interface: Allocations, Update
- Kubernetes implementation in `pkg/storage/kubernetes/` handles:
  - IPPool CRDs for tracking allocations per IP range
  - OverlappingRangeIPReservation CRDs for cross-range IP uniqueness
  - Retry logic with exponential backoff for concurrent updates

**Configuration** (`pkg/config/`)
- Parses CNI configuration from JSON
- Supports both inline config and flat-file configuration (`/etc/cni/net.d/whereabouts.d/whereabouts.conf`)
- Handles range specifications: single CIDR, range with start/end, ipRanges for multi-IP/dual-stack

**IP Reconciler** (`pkg/reconciler/`)
- Runs as a Kubernetes CronJob to clean up stranded IP allocations
- Compares allocated IPs against running pods
- Deallocates IPs for pods that no longer exist (handles node failures, force deletes)
- Reconciler cron expression configurable via ConfigMap or environment variable

**Controllers**
- **IP Reconciliation Controller** (`cmd/controlloop/`): Watches pods and reconciles IP allocations
- **Node Slice Controller** (`cmd/nodeslicecontroller/`): Experimental "Fast IPAM" feature that pre-allocates IP slices per node to reduce contention

### Custom Resource Definitions

**IPPool** (`pkg/api/whereabouts.cni.cncf.io/v1alpha1/ippool_types.go`)
- Stores IP allocations for a specific range
- Key format: `<namespace>-<network-name>-<normalized-range>`
- Contains array of IPReservation entries with IP, PodRef, ContainerID, IfName

**OverlappingRangeIPReservation** (`pkg/api/whereabouts.cni.cncf.io/v1alpha1/overlappingrangeipreservation_types.go`)
- Ensures IP uniqueness across overlapping ranges when `enable_overlapping_ranges: true`
- Prevents same IP from being allocated in different ranges that overlap

**NodeSlicePool** (`pkg/api/whereabouts.cni.cncf.io/v1alpha1/nodeslicepool_types.go`)
- Experimental: Tracks node-specific IP slice allocations for Fast IPAM
- Enabled by setting `node_slice_size` in IPAM config

### Storage Backend Flow

1. **IP Allocation (ADD command)**:
   - Load IPAM config from CNI stdin
   - Create Kubernetes IPAM client with pod context
   - Get or create IPPool CR for the range
   - Check if podRef+ifName already allocated (idempotent)
   - Find lowest available IP using `IterateForAssignment`
   - Update IPPool CR with new reservation (with retries for conflicts)
   - Handle overlapping range checks if enabled
   - Return allocated IP to CNI runtime

2. **IP Deallocation (DEL command)**:
   - Load IPAM config
   - Get IPPool CR
   - Remove reservation matching containerID+ifName
   - Update IPPool CR
   - Clean up overlapping range reservation if present

3. **Conflict Resolution**:
   - Uses optimistic locking with Kubernetes resource versions
   - Retries up to DatastoreRetries (100) times on conflict
   - Exponential backoff between retries

### Key Packages

- `pkg/allocate`: IP assignment algorithms, iteration logic
- `pkg/iphelpers`: IP address arithmetic, range calculations, CIDR parsing
- `pkg/storage/kubernetes`: Kubernetes CRD-based storage implementation
- `pkg/types`: Core data structures (RangeConfiguration, IPReservation, IPAMConfig)
- `pkg/config`: Configuration parsing and validation
- `pkg/reconciler`: Cleanup and reconciliation logic
- `pkg/controlloop`: Kubernetes controllers (pod watcher, node slice management)
- `pkg/generated`: Auto-generated Kubernetes clientsets, informers, listers

## Testing Strategy

**Unit Tests**: Ginkgo/Gomega framework used extensively
- Test files colocated with implementation: `*_test.go`
- Use fake Kubernetes clients for testing without cluster
- Run with `make test` which includes `go vet` and `staticcheck`

**E2E Tests**: Located in `e2e/` directory
- Requires a running Kubernetes cluster (create with `make kind`)
- Tests actual CNI plugin behavior with pods
- Tests in `e2e/e2e_test.go` and `e2e/e2e_node_slice/` for Fast IPAM
- Pool consistency tests in `e2e/poolconsistency/`

**Test Utilities**:
- `e2e/client/`: Kubernetes client wrappers for test operations
- `e2e/entities/`: Test entity generators (pods, deployments, etc.)
- `e2e/testenvironment/`: Test environment configuration

## Important Development Notes

### Fast IPAM (Experimental)
- Enabled by adding `node_slice_size` field to IPAM config
- Requires running the node slice controller: `doc/crds/node-slice-controller.yaml`
- Controller and daemonset must run in same namespace as NetworkAttachmentDefinitions
- Pre-allocates IP slices per node to reduce allocation contention in large clusters

### Configuration Hierarchy
Whereabouts merges configuration from multiple sources (priority: high to low):
1. Inline IPAM configuration in NetworkAttachmentDefinition
2. CNI config file parameters
3. Flat file configuration at `configuration_path` or default locations

### Idempotent IP Allocation
The plugin checks for existing allocations by `podRef` and `ifName` before allocating. This ensures that re-running ADD for the same pod interface returns the same IP, which is critical for CNI plugin reliability.

### Storage Backend Considerations
- Default and recommended: Kubernetes CRDs (no additional infrastructure)
- Legacy: etcd (deprecated, requires separate etcd cluster)
- Overlapping range protection only works with Kubernetes backend

### Network Names
Use `network_name` parameter to allow multiple independent allocations from the same CIDR in multi-tenant scenarios. This creates separate IPPool CRs per network name.
