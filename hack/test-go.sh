#!/usr/bin/env bash
# single test: go test -v ./pkg/storage/
# without cache: go test -count=1 -v ./pkg/storage/
echo "Stopping and removing etcd server..."
docker stop etcd
docker rm etcd
echo "Start etcd server..."
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
echo "Running go tests..."
go test -v ./...
