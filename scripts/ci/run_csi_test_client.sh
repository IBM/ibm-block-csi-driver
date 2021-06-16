#!/bin/bash -xe

[ $# -ne 3 ] && { echo "Usage $0 : container_name folder_for_junit"  ; exit 1; }

if [ $3 = 'community_svc' ] ; then
    csi_params='csi_params_thin'
else
    csi_params='csi_params'
fi
#/tmp/k8s_dir is the directory of the csi grpc\unix socket that shared between csi server and csi-test docker
docker build -f Dockerfile-csi-test --build-arg CSI_PARAMS=${csi_params} -t csi-sanity-test .  && docker run --user=root -e STORAGE_ARRAYS=${STORAGE_ARRAYS} -e USERNAME=${USERNAME} -e PASSWORD=${PASSWORD}  -e POOL_NAME=${POOL_NAME} -v /tmp/k8s_dir:/tmp/k8s_dir:rw  -v$2:/tmp/test_results:rw --rm  --name $1 csi-sanity-test

