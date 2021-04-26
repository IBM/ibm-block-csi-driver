#!/bin/bash -xe

# create the /tmp/k8s-dir where the grpc unix socket will be.
if [ ! -d /tmp/k8s_dir ]; then
 mkdir /tmp/k8s_dir
fi 

chmod 777 /tmp/k8s_dir

[ $# -ne 1 ] && { echo "Usage $0 : container_name"  ; exit 1; }

docker build -f Dockerfile-csi-node -t csi-node . &&  \
docker run --privileged -v /:/host:rshared -v /tmp/k8s_dir:/tmp/k8s_dir:rw \
-d --network host --ipc="host" --name $1 \
csi-node --csi-endpoint unix://tmp/k8s_dir/nodecsi --hostname=`hostname` --config-file-path=./config.yaml
