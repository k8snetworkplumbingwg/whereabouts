# Contributing to Whereabouts

Thank you for your interest in contributing to the Whereabouts IPAM CNI plugin!

## Getting Started

1. **Fork** the repository and clone your fork.
2. **Install Go** (see `go.mod` for the required version).
3. **Build** the binary:
   ```bash
   make build
   ```
4. **Run tests**:
   ```bash
   make test              # Full: build + vet + staticcheck + tests
   make test-skip-static  # Faster iteration (skip staticcheck)
   go test -v ./pkg/allocate/  # Single package
   ```

## Development Workflow

### Local Kubernetes Cluster

Create a local [kind](https://kind.sigs.k8s.io/) cluster with Whereabouts installed:

```bash
make kind                 # Default: 2 worker nodes
make kind COMPUTE_NODES=3 # Custom worker count
```

### Code Generation

After modifying CRD types in `api/`:

```bash
make generate-api         # Regenerate deepcopy + manifests
./hack/update-codegen.sh  # Regenerate clientsets, informers, listers
./hack/verify-codegen.sh  # Verify generated code is up-to-date
```

### Dependencies

```bash
make update-deps  # go mod tidy && go mod vendor && go mod verify
```

## Code Conventions

### Error Handling

- Wrap errors with `fmt.Errorf("context: %w", err)` — use `%w` for proper error wrapping in new code (operator, webhooks, controllers).
- Legacy CNI plugin code (`pkg/`, `cmd/whereabouts.go`) still uses `%s` — migrate to `%w` opportunistically.
- Use `logging.Errorf("msg: %v", err)` to both log AND return an error in one call.
- When discarding the returned error: `_ = logging.Errorf(...)`.
- Custom error types use struct + `Error() string` + constructor (e.g. `NewInvalidPluginError()`).

### Logging

- Use `logging.Debugf` / `logging.Verbosef` / `logging.Errorf` from `pkg/logging/`.
- Do **not** introduce third-party loggers.

### Testing

- **Ginkgo v2** + Gomega with dot-imports:
  ```go
  . "github.com/onsi/ginkgo/v2"
  . "github.com/onsi/gomega"
  ```
- Suite bootstrap: `RegisterFailHandler(Fail); RunSpecs(t, "Suite Name")`
- Use `fake.NewClientset(...)` from `client-go/kubernetes/fake` for unit tests.
- controller-runtime `envtest` is used for reconciler and webhook integration tests.
- Never edit `zz_generated.deepcopy.go` manually.

### Import Aliases

| Alias                    | Package                                              |
|--------------------------|------------------------------------------------------|
| `whereaboutstypes`       | `pkg/types`                                          |
| `whereaboutsv1alpha1`    | `api/whereabouts.cni.cncf.io/v1alpha1` |

### JSON Tags

All configuration struct JSON tags use **snake_case** (e.g., `range_start`, `enable_overlapping_ranges`).

## Running E2E Tests

End-to-end tests verify the full CNI plugin lifecycle against a real Kubernetes cluster.

1. Install [godotenv](https://github.com/joho/godotenv): `go install github.com/joho/godotenv/cmd/godotenv@latest`
2. Create a kind cluster: `make kind` (or `make kind COMPUTE_NODES=3` for multi-node tests)
3. Create `e2e/.env` with: `KUBECONFIG=$HOME/.kube/config`
4. Run the tests:
   ```bash
   cd e2e && godotenv -f .env go test -v . -timeout=1h
   ```

For Fast IPAM (NodeSlice) e2e tests:
```bash
cd e2e/e2e_node_slice && godotenv -f ../e2e/.env go test -v . -timeout=1h
```

## Pull Requests

- Keep PRs focused — one logical change per PR.
- Include tests for new functionality.
- Run `make test` before submitting.
- Update documentation if you change configuration or behavior.

## Reporting Issues

Open an issue on GitHub with:
- Whereabouts version / image tag
- Kubernetes version
- Network configuration (NAD spec, IPAM config)
- CNI log output (usually `/var/log/whereabouts.log`)

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.
