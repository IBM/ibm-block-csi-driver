#!/bin/bash -xe

if [ ! -d /tmp/k8s_dir ]; then
 mkdir /tmp/k8s_dir
fi 

chmod 777 /tmp/k8s_dir

docker build  -f Dockerfile-csi-controller -t csi-controller . &&  docker run -v /tmp/k8s_dir:/tmp/k8s_dir:rw  --rm --name $1  csi-controller -e unix://tmp/k8s_dir/f

