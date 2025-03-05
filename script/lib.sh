CNI_BIN_DIR=${CNI_BIN_DIR:-"/host/opt/cni/bin/"}
WHEREABOUTS_KUBECONFIG_FILE_HOST=${WHEREABOUTS_KUBECONFIG_FILE_HOST:-"/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig"}
CNI_CONF_DIR=${CNI_CONF_DIR:-"/host/etc/cni/net.d"}
WHEREABOUTS_RECONCILER_CRON=${WHEREABOUTS_RECONCILER_CRON:-30 4 * * *}

# Make a whereabouts.d directory (for our kubeconfig)

mkdir -p $CNI_CONF_DIR/whereabouts.d
WHEREABOUTS_KUBECONFIG=$CNI_CONF_DIR/whereabouts.d/whereabouts.kubeconfig
WHEREABOUTS_CONF_FILE=$CNI_CONF_DIR/whereabouts.d/whereabouts.conf
WHEREABOUTS_KUBECONFIG_LITERAL=$(echo "$WHEREABOUTS_KUBECONFIG" | sed -e s'|/host||')

SERVICE_ACCOUNT_PATH=/var/run/secrets/kubernetes.io/serviceaccount
KUBE_CA_FILE=${KUBE_CA_FILE:-$SERVICE_ACCOUNT_PATH/ca.crt}
SERVICE_ACCOUNT_TOKEN=$(cat $SERVICE_ACCOUNT_PATH/token)
SERVICE_ACCOUNT_TOKEN_PATH=$SERVICE_ACCOUNT_PATH/token
SKIP_TLS_VERIFY=${SKIP_TLS_VERIFY:-false}


function log()
{
    echo "$(date -Iseconds) ${1}"
}

function error()
{
    log "ERR:  {$1}"
}

function warn()
{
    log "WARN: {$1}"
}


function generateKubeConfig {
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
# Kubeconfig file for the Whereabouts CNI plugin.
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
    token: "${SERVICE_ACCOUNT_TOKEN}"
contexts:
- name: whereabouts-context
  context:
    cluster: local
    user: whereabouts
    namespace: ${WHEREABOUTS_NAMESPACE}
current-context: whereabouts-context
EOF

else
  warn "Doesn't look like we're running in a kubernetes environment (no serviceaccount token)"
fi

}

function generateWhereaboutsConf {

  touch $WHEREABOUTS_CONF_FILE
  chmod ${KUBECONFIG_MODE:-600} $WHEREABOUTS_CONF_FILE
  cat > $WHEREABOUTS_CONF_FILE <<EOF
{
  "datastore": "kubernetes",
  "kubernetes": {
    "kubeconfig": "${WHEREABOUTS_KUBECONFIG_LITERAL}"
  },
  "reconciler_cron_expression": "${WHEREABOUTS_RECONCILER_CRON}"
}
EOF

}

function get_token_md5sum {
  md5sum "$SERVICE_ACCOUNT_TOKEN_PATH" | awk '{print $1}'
}

function get_ca_file_md5sum {
  if [ ! -f "$KUBE_CA_FILE" ]; then
    echo ""
    return
  fi
  md5sum "$KUBE_CA_FILE" | awk '{print $1}'
}
