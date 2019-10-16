## Development notes

Run `go mod` with:

```
go mod vendor
```

## Running with CNI's `docker-run.sh`


Put plugins in `/opt/cni/bin` and configs in `/etc/cni/net.d` -- README config should be fine.

```
export CNI_PATH=/opt/cni/bin/
export NETCONFPATH=/etc/cni/net.d
CNI_PATH=$CNI_PATH ./docker-run.sh --rm busybox:latest ifconfig
```

## Running in Kube

...Remember to replace with your etcd host.

Create the config...

```
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
        "etcd_host": "10.107.83.18:2379",
        "log_file" : "/tmp/whereabouts.log",
        "log_level" : "debug",
        "gateway": "192.168.2.1"
      }
    }'
EOF
```

Kick off a pod...

```
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

