#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
DIFFROOT_PKG="${SCRIPT_ROOT}/pkg"
DIFFROOT_API="${SCRIPT_ROOT}/api"
TMPDIR="$(mktemp -d -t "$(basename "$0").XXXXXX")"
TMP_DIFFROOT_PKG="${TMPDIR}/pkg"
TMP_DIFFROOT_API="${TMPDIR}/api"

cleanup() {
  rm -rf "${TMPDIR}"
}
trap "cleanup" EXIT SIGINT

mkdir -p "${TMP_DIFFROOT_PKG}" "${TMP_DIFFROOT_API}"
cp -a "${DIFFROOT_PKG}/." "${TMP_DIFFROOT_PKG}/"
cp -a "${DIFFROOT_API}/." "${TMP_DIFFROOT_API}/"

"${SCRIPT_ROOT}/hack/update-codegen.sh"

echo "diffing pkg/ against freshly generated codegen"
ret=0
if ! diff -Naupr "${DIFFROOT_PKG}" "${TMP_DIFFROOT_PKG}"; then
  ret=1
fi

echo "diffing api/ against freshly generated codegen"
if ! diff -Naupr "${DIFFROOT_API}" "${TMP_DIFFROOT_API}"; then
  ret=1
fi

if [[ $ret -eq 0 ]]; then
    # Keep working tree clean when generated code already matches snapshot.
    cp -a "${TMP_DIFFROOT_PKG}/." "${DIFFROOT_PKG}/"
    cp -a "${TMP_DIFFROOT_API}/." "${DIFFROOT_API}/"
    echo "pkg/ and api/ up to date."
else
    echo "pkg/ or api/ is out of date. Please run hack/update-codegen.sh"
    exit 1
fi

