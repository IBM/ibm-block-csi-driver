#!/bin/bash -xe
echo 'lihitest1'
# create the /tmp/k8s-dir where the grpc unix socket will be.
if [ ! -d /tmp/k8s_dir ]; then
 mkdir /tmp/k8s_dir
fi 
echo 'lihitest2'
chmod 777 /tmp/k8s_dir
echo 'lihitest3'
[ $# -ne 1 ] && { echo "Usage $0 : container_name"  ; exit 1; }
echo 'lihitest4'
docker build -f Dockerfile-csi-controller -t csi-controller . &&  \
docker run -v /tmp/k8s_dir:/tmp/k8s_dir:rw -d --network host --name $1  csi-controller -e unix://tmp/k8s_dir/f

