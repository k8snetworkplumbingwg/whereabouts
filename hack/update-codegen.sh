#!/usr/bin/env bash

# Copyright 2017 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(dirname "${BASH_SOURCE[0]}")"
SCRIPT_ROOT="${SCRIPT_DIR}/.."
# Use the code-generator version pinned in go.mod (via tools.go) from the module cache.
CODEGEN_PKG="${CODEGEN_PKG:-$(cd "${SCRIPT_ROOT}" && go mod download k8s.io/code-generator && go list -m -f '{{.Dir}}' k8s.io/code-generator)}"

source "${CODEGEN_PKG}/kube_codegen.sh"

THIS_PKG="github.com/k8snetworkplumbingwg/whereabouts"

kube::codegen::gen_openapi \
    --output-dir "${SCRIPT_ROOT}/pkg/generated/openapi" \
    --output-pkg "${THIS_PKG}/pkg/generated/openapi" \
    --report-filename "${SCRIPT_ROOT}/hack/openapi-violations.list" \
    --update-report \
    --boilerplate "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
    "${SCRIPT_ROOT}/api"

# Build OpenAPI schema JSON from the generated Go definitions.
# applyconfiguration-gen needs this to populate the structured-merge-diff
# schema used by fake.NewClientset().
OPENAPI_JSON="${SCRIPT_ROOT}/bin/openapi-schema.json"
mkdir -p "${SCRIPT_ROOT}/bin"
(cd "${SCRIPT_ROOT}" && go build -o bin/openapi-schema ./hack/tools/openapi-schema/)
"${SCRIPT_ROOT}/bin/openapi-schema" > "${OPENAPI_JSON}"

kube::codegen::gen_client \
    --with-watch \
    --with-applyconfig \
    --applyconfig-openapi-schema "${OPENAPI_JSON}" \
    --output-dir "${SCRIPT_ROOT}/pkg/generated" \
    --output-pkg "${THIS_PKG}/pkg/generated" \
    --boilerplate "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
    "${SCRIPT_ROOT}/api"
