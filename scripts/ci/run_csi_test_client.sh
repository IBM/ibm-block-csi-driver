#!/bin/bash -xe

[ $# -ne 2 ] && { echo "Usage $0 : container_name folder_for_junit"  ; exit 1; }
echo "__ $0 __ $1 __ $2 __ $3 __"
#/tmp/k8s_dir is the directory of the csi grpc\unix socket that shared between csi server and csi-test docker 
if [ $3 = 'community_svc' ] ; then
    docker build -f Dockerfile-csi-svc-test -t csi-sanity-test .  && docker run --user=root -e STORAGE_ARRAYS=${STORAGE_ARRAYS} -e USERNAME=${USERNAME} -e PASSWORD=${PASSWORD}  -e POOL_NAME=${POOL_NAME} -v /tmp/k8s_dir:/tmp/k8s_dir:rw  -v$2:/tmp/test_results:rw --rm  --name $1 csi-sanity-test
then
    docker build -f Dockerfile-csi-test -t csi-sanity-test .  && docker run --user=root -e STORAGE_ARRAYS=${STORAGE_ARRAYS} -e USERNAME=${USERNAME} -e PASSWORD=${PASSWORD}  -e POOL_NAME=${POOL_NAME} -v /tmp/k8s_dir:/tmp/k8s_dir:rw  -v$2:/tmp/test_results:rw --rm  --name $1 csi-sanity-test
fi
