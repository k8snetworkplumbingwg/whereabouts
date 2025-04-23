#!/bin/sh

set -u -e

# Inspired by: https://github.com/intel/multus-cni/blob/83556f49bd6706a885eda847210b542669279cd0/images/entrypoint.sh#L161-L222
#
# Copyright (c) 2018 Intel Corporation
#
# Licensed under the Apache License, Version 2.0 (the "License");
#
#SPDX-License-Identifier: Apache-2.0

source /lib.sh

# Setup our logging routines


# -------------------Generate a "kube-config"
generateKubeConfig
export LAST_SERVICEACCOUNT_MD5SUM="$(get_token_md5sum)"
export LAST_KUBE_CA_FILE_MD5SUM="$(get_ca_file_md5sum)"
# ------------------ end Generate a "kube-config"

# ----------------- Generate a whereabouts conf
generateWhereaboutsConf
# ---------------- End generate a whereabouts conf


# copy whereabouts to the cni bin dir
cp -f /whereabouts $CNI_BIN_DIR

# ---------------------- end generate a "kube-config".

# Unless told otherwise, sleep forever.
# This prevents Kubernetes from restarting the pod repeatedly.
should_sleep=${SLEEP:-"true"}
echo "Done configuring CNI.  Sleep=$should_sleep"
while [ "$should_sleep" == "true"  ]; do
    sleep 1000000000000
done
