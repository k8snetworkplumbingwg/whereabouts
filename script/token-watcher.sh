#!/bin/sh

set -u -e

source lib.sh

echo "Sleep and Watching for service account token and CA file changes..."
# enter sleep/watch loop
while true; do
  # Check the md5sum of the service account token and ca.
  svcaccountsum=$(md5sum $SERVICE_ACCOUNT_TOKEN_PATH | awk '{print $1}')
  casum=$(md5sum $KUBE_CA_FILE | awk '{print $1}')
  if [ "$svcaccountsum" != "$LAST_SERVICEACCOUNT_MD5SUM" ] || [ "$casum" != "$LAST_KUBE_CA_FILE_MD5SUM" ]; then
    # log "Detected service account or CA file change, regenerating kubeconfig..."
    generateKubeConfig
  fi

  sleep 1h
done
