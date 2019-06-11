#!/bin/bash -xe

#docker build  -f Dockerfile-csi-controller -t csi-controller . &&  docker run  --name $1  csi-controller -e [::]:4444

docker build  -f Dockerfile-csi-controller -t csi-controller . &&  docker run -v /tmp/k8s_dir:/tmp/k8s_dir:rw  --name $1  csi-controller -e /tmp/k8s_dir/f

