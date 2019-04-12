## Development tips

Run etcd.

```
docker stop etcd
docker rm etcd
docker run -dt \
-p 2379:2379 \
-p 2380:2380 \
--name etcd quay.io/coreos/etcd:latest \
/usr/local/bin/etcd \
-listen-client-urls http://0.0.0.0:2379 \
--data-dir=/etcd-data --name node1 \
--initial-advertise-peer-urls http://127.0.0.1:2380 --listen-peer-urls http://127.0.0.1:2380 \
--advertise-client-urls http://127.0.0.1:2379 \
--initial-cluster node1=http://127.0.0.1:2380
```

Manipulate etcd.

```
docker exec -it etcd /bin/sh
export ETCDCTL_API=3
# etcdctl del /192.168.1.0/24
```

## Development notes

Run glide with:

```
glide install --strip-vcs --strip-vendor
```

(Otherwise, you might run into issues with nested vendored packages)


## Oddities with vendored stuff.

Had to override the glide.lock hash for `golang.org/x/sys` with `1c9583448a9c3aa0f9a6a5241bf73c0bd8aafded` found it in [this github comment](https://github.com/grpc/grpc-go/issues/2181#issuecomment-414324934)

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

