#!/bin/sh

set -u -e

# Inspired by: https://github.com/intel/multus-cni/blob/83556f49bd6706a885eda847210b542669279cd0/images/entrypoint.sh#L161-L222
#
# Copyright (c) 2018 Intel Corporation
#
# Licensed under the Apache License, Version 2.0 (the "License");
#
#SPDX-License-Identifier: Apache-2.0

CNI_BIN_DIR=${CNI_BIN_DIR:-"/host/opt/cni/bin/"}
WHEREABOUTS_KUBECONFIG_FILE_HOST=${WHEREABOUTS_KUBECONFIG_FILE_HOST:-"/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"}
CNI_CONF_DIR=${CNI_CONF_DIR:-"/host/etc/cni/net.d"}

# Make a whereabouts.d directory (for our kubeconfig)

mkdir -p $CNI_CONF_DIR/whereabouts.d
WHEREABOUTS_KUBECONFIG=$CNI_CONF_DIR/whereabouts.d/whereabouts.kubeconfig
WHEREABOUTS_FLATFILE=$CNI_CONF_DIR/whereabouts.d/whereabouts.conf
WHEREABOUTS_KUBECONFIG_LITERAL=$(echo "$WHEREABOUTS_KUBECONFIG" | sed -e s'|/host||')

# ------------------------------- Generate a "kube-config"
SERVICE_ACCOUNT_PATH=/var/run/secrets/kubernetes.io/serviceaccount
KUBE_CA_FILE=${KUBE_CA_FILE:-$SERVICE_ACCOUNT_PATH/ca.crt}
SERVICEACCOUNT_TOKEN=$(cat $SERVICE_ACCOUNT_PATH/token)
SKIP_TLS_VERIFY=${SKIP_TLS_VERIFY:-false}

# Setup our logging routines

function log()
{
    echo "$(date --iso-8601=seconds) ${1}"
}

function error()
{
    log "ERR:  {$1}"
}

function warn()
{
    log "WARN: {$1}"
}


# Check if we're running as a k8s pod.
if [ -f "$SERVICE_ACCOUNT_PATH/token" ]; then
  # We're running as a k8d pod - expect some variables.
  if [ -z ${KUBERNETES_SERVICE_HOST} ]; then
    error "KUBERNETES_SERVICE_HOST not set"; exit 1;
  fi
  if [ -z ${KUBERNETES_SERVICE_PORT} ]; then
    error "KUBERNETES_SERVICE_PORT not set"; exit 1;
  fi

  if [ "$SKIP_TLS_VERIFY" == "true" ]; then
    TLS_CFG="insecure-skip-tls-verify: true"
  elif [ -f "$KUBE_CA_FILE" ]; then
    TLS_CFG="certificate-authority-data: $(cat $KUBE_CA_FILE | base64 | tr -d '\n')"
  fi

  # Kubernetes service address must be wrapped if it is IPv6 address
  KUBERNETES_SERVICE_HOST_WRAP=$KUBERNETES_SERVICE_HOST
  if [ "$KUBERNETES_SERVICE_HOST_WRAP" != "${KUBERNETES_SERVICE_HOST_WRAP#*:[0-9a-fA-F]}" ]; then
    KUBERNETES_SERVICE_HOST_WRAP=\[$KUBERNETES_SERVICE_HOST_WRAP\]
  fi

  # Write a kubeconfig file for the CNI plugin.  Do this
  # to skip TLS verification for now.  We should eventually support
  # writing more complete kubeconfig files. This is only used
  # if the provided CNI network config references it.
  touch $WHEREABOUTS_KUBECONFIG
  chmod ${KUBECONFIG_MODE:-600} $WHEREABOUTS_KUBECONFIG
  cat > $WHEREABOUTS_KUBECONFIG <<EOF
# Kubeconfig file for Multus CNI plugin.
apiVersion: v1
kind: Config
clusters:
- name: local
  cluster:
    server: ${KUBERNETES_SERVICE_PROTOCOL:-https}://${KUBERNETES_SERVICE_HOST_WRAP}:${KUBERNETES_SERVICE_PORT}
    $TLS_CFG
users:
- name: whereabouts
  user:
    token: "${SERVICEACCOUNT_TOKEN}"
contexts:
- name: whereabouts-context
  context:
    cluster: local
    user: whereabouts
    namespace: ${WHEREABOUTS_NAMESPACE}
current-context: whereabouts-context
EOF

  touch $WHEREABOUTS_FLATFILE
  chmod ${KUBECONFIG_MODE:-600} $WHEREABOUTS_FLATFILE
  cat > $WHEREABOUTS_FLATFILE <<EOF
{
  "datastore": "kubernetes",
  "kubernetes": {
    "kubeconfig": "${WHEREABOUTS_KUBECONFIG_LITERAL}"
  },
  "reconciler_cron_expression": "30 4 * * *"
}
EOF

else
  warn "Doesn't look like we're running in a kubernetes environment (no serviceaccount token)"
fi

# copy whereabouts to the cni bin dir
cp -f /whereabouts $CNI_BIN_DIR

# ---------------------- end Generate a "kube-config".

# Unless told otherwise, sleep forever.
# This prevents Kubernetes from restarting the pod repeatedly.
should_sleep=${SLEEP:-"true"}
echo "Done configuring CNI.  Sleep=$should_sleep"
while [ "$should_sleep" == "true"  ]; do
    sleep 1000000000000
done
