#!/bin/sh

set -u -e

source lib.sh

echo "Sleep and Watching for service account token and CA file changes..."
# enter sleep/watch loop
while true; do
  # Check the md5sum of the service account token and ca.
  svcaccountsum="$(get_token_md5sum)"
  casum="$(get_ca_file_md5sum)"
  if [ "$svcaccountsum" != "$LAST_SERVICEACCOUNT_MD5SUM" ] || ! [ "$SKIP_TLS_VERIFY" == "true" ] && [ "$casum" != "$LAST_KUBE_CA_FILE_MD5SUM" ]; then
    log "Detected service account or CA file change, regenerating kubeconfig..."
    generateKubeConfig
    LAST_SERVICEACCOUNT_MD5SUM="$svcaccountsum"
    LAST_KUBE_CA_FILE_MD5SUM="$casum"
  fi

  sleep 1s
done
