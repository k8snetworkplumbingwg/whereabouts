#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
DIFFROOT="${SCRIPT_ROOT}/pkg"
TMP_DIFFROOT="$(mktemp -d -t "$(basename "$0").XXXXXX")/pkg"

cleanup() {
  rm -rf "${TMP_DIFFROOT}"
}
trap "cleanup" EXIT SIGINT

cleanup

mkdir -p "${TMP_DIFFROOT}"
cp -a "${DIFFROOT}"/* "${TMP_DIFFROOT}"

"${SCRIPT_ROOT}/hack/update-codegen.sh"

echo "diffing ${DIFFROOT} against freshly generated codegen"
ret=0
diff -Naupr "${DIFFROOT}" "${TMP_DIFFROOT}" || ret=$?
cp -a "${TMP_DIFFROOT}"/* "${DIFFROOT}"
if [[ $ret -eq 0 ]]
then
    echo "${DIFFROOT} up to date."
else
    echo "${DIFFROOT} is out of date. Please run hack/update-codegen.sh"
    exit 1
fi

