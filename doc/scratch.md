
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