# Development Notes

## Quick Reference

| Task | Command |
|------|---------|
| Build binary | `make build` |
| Build Docker image | `make docker-build` |
| Run all tests | `make test` |
| Run tests (skip staticcheck) | `make test-skip-static` |
| Run single package tests | `go test -v ./pkg/allocate/` |
| Run tests without cache | `go test -count=1 -v ./pkg/storage/` |
| Update dependencies | `make update-deps` |
| Regenerate API code | `make generate-api` |
| Update clientsets/informers/listers | `./hack/update-codegen.sh` |
| Verify generated code | `./hack/verify-codegen.sh` |
| Create local cluster | `make kind` |
| Deploy operator to cluster | `make deploy` |

## Dependencies

```bash
go mod tidy
go mod vendor
go mod verify
```

Or use the convenience target: `make update-deps`

## Static Analysis

The project uses `go vet` and [staticcheck](https://staticcheck.io/). Both are
run by `make test`.  To skip staticcheck for faster iteration:

```bash
make test-skip-static
```

To run them independently:

```bash
go vet ./...
./bin/staticcheck ./...
```

## Testing Framework

* **Unit tests** use [Ginkgo v2](https://onsi.github.io/ginkgo/) + [Gomega](https://onsi.github.io/gomega/) with dot-imports.
* Some packages (e.g., `pkg/storage/kubernetes/`) use standard `testing.T` table-driven tests.
* controller-runtime `envtest` is used for reconciler and webhook integration tests.
* Fake Kubernetes clients (`fake.NewClientset(...)`) are used for unit testing without a cluster.

### envtest Tests

The reconciler and webhook tests in `internal/controller/` and `internal/webhook/`
use controller-runtime's [envtest](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/envtest)
package. This starts a local API server and etcd, making the tests behave like
integration tests:

```bash
# Run reconciler tests (envtest starts automatically)
go test -v ./internal/controller/

# Run webhook tests
go test -v ./internal/webhook/

# Run all internal tests
go test -v ./internal/...
```

The `envtest` binaries (API server, etcd) are downloaded automatically by the
test suite setup. No manual installation is required.

### Test Entity Helpers

Test entity helpers for reconciler tests live alongside production code with a
`//go:build test` build tag. These are only included in test binaries and provide
convenience constructors for IPPool, NodeSlicePool, and other CRD objects.

## Running in Kube (Manual Testing)

Create a NetworkAttachmentDefinition:

```bash
cat <<EOF | kubectl create -f -
apiVersion: "k8s.cni.cncf.io/v1"
kind: NetworkAttachmentDefinition
metadata:
  name: macvlan-conf
spec:
  config: '{
      "cniVersion": "0.3.0",
      "name": "whereaboutsexample",
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "whereabouts",
        "range": "192.168.2.225/28",
        "log_file" : "/tmp/whereabouts.log",
        "log_level" : "debug",
        "gateway": "192.168.2.1"
      }
    }'
EOF
```

Kick off a pod:

```bash
cat <<EOF | kubectl create -f -
apiVersion: v1
kind: Pod
metadata:
  name: samplepod
  annotations:
    k8s.v1.cni.cncf.io/networks: macvlan-conf
spec:
  containers:
  - name: samplepod
    command: ["/bin/bash", "-c", "sleep 2000000000000"]
    image: dougbtv/centos-network
EOF
```

Verify the IP allocation:

```bash
# Check the pod's network-status annotation
kubectl get pod samplepod -o jsonpath='{.metadata.annotations.k8s\.v1\.cni\.cncf\.io/network-status}' | jq .

# Check the IPPool CRD for the allocation
kubectl get ippools -A -o yaml
```

## Operator Development

### Running the Operator Locally

For rapid iteration on reconciler or webhook code, you can run the operator
outside the cluster while pointing at a kind cluster:

```bash
# Build the operator binary
go build -o bin/whereabouts-operator ./cmd/operator/

# Run the controller subcommand locally
./bin/whereabouts-operator controller \
  --namespace=kube-system \
  --reconcile-interval=10s \
  --leader-elect=false \
  --webhook-port=9443 \
  --cert-dir=/tmp/webhook-certs \
  --metrics-bind-address=:8080 \
  --health-probe-bind-address=:8081
```

Note: When running locally, the webhook server won't be reachable from the API
server unless you set up port-forwarding or register a webhook pointing to your
local machine. For webhook development, prefer `envtest`-based tests.

### Deploying to a Kind Cluster

```bash
# Build the image and load it into kind
make docker-build
kind load docker-image ghcr.io/k8snetworkplumbingwg/whereabouts:latest

# Deploy with kustomize
make deploy

# Verify the operator is running
kubectl get pods -n kube-system -l app=whereabouts-controller
kubectl logs -n kube-system deploy/whereabouts-controller-manager -f
```

### Debugging Reconcilers

Add `-v=4` (or higher) to the operator command args for verbose controller-runtime
logging:

```yaml
command:
- /whereabouts-operator
- controller
- -v=4
```

Useful log queries:

```bash
# Watch reconciliation events
kubectl logs -n kube-system deploy/whereabouts-controller-manager -f | grep -E "reconcil|orphan|cleanup"

# Watch webhook events
kubectl logs -n kube-system deploy/whereabouts-controller-manager -f | grep -i webhook

# Watch certificate rotation events
kubectl logs -n kube-system deploy/whereabouts-controller-manager -f | grep -i cert
```

### Inspecting CRDs

```bash
# List IP pools and their allocations
kubectl get ippools -A -o yaml

# Check overlapping range reservations
kubectl get overlappingrangeipreservations -A

# Check node slice pools (Fast IPAM)
kubectl get nodeslicepools -A -o yaml

# Count allocations in a pool
kubectl get ippool -n kube-system <pool-name> -o jsonpath='{.spec.allocations}' | jq 'length'
```

## Running E2E Tests Locally

1. Install [godotenv](https://github.com/joho/godotenv):
   ```bash
   go install github.com/joho/godotenv/cmd/godotenv@latest
   ```
2. Create a kind cluster:
   ```bash
   make kind                 # Default: 2 worker nodes
   make kind COMPUTE_NODES=3 # 3 workers (needed for drain/eviction tests)
   ```
3. Create `e2e/.env`:
   ```bash
   # Find your kubeconfig path
   [[ ! -z "$KUBECONFIG" ]] && echo "$KUBECONFIG" || echo "$HOME/.kube/config"
   # Create the .env file
   echo "KUBECONFIG=$HOME/.kube/config" > e2e/.env
   ```
4. Run the main e2e tests:
   ```bash
   cd e2e && godotenv -f .env go test -v . -timeout=1h
   ```
5. Run Fast IPAM (NodeSlice) e2e tests:
   ```bash
   cd e2e/e2e_node_slice && godotenv -f ../e2e/.env go test -v . -timeout=1h
   ```

### E2E Test Coverage

The e2e test suite covers:

- **IPv4/IPv6/Dual-stack**: All features are tested across IP versions. Dual-stack tests
  use `ipRanges` with both v4 and v6 CIDRs.
- **Core features**: Range allocation, exclude ranges, gateway exclusion, L3 mode,
  optimistic IPAM, named networks, multi-pool, overlapping ranges.
- **Advanced features**: Preferred/sticky IP, small subnets (/31, /32), pool exhaustion
  recovery, reallocation cycles.
- **Edge cases**: Node cordon + eviction, pod eviction via Policy API, rapid pod churn,
  multi-interface cleanup, StatefulSet scale down/up, concurrent burst creation.
- **Pool consistency**: Verification that IP pool CRDs accurately reflect running pod
  allocations after various scenarios.

## Scale Testing

The scale test script at `script/scale-test.sh` tests IP allocation under load:

1. Ensure you have a running cluster (e.g., `make kind COMPUTE_NODES=3`).
2. The script uses the yamls in `yamls/`:
   - `whereaboutsScaleNAD.yaml` — NetworkAttachmentDefinition for the test
   - `scaleTestDeployment.yaml` — Deployment with configurable replicas
3. Modify the `replicas` value in `scaleTestDeployment.yaml` to control the test size.
4. Run: `./script/scale-test.sh`
