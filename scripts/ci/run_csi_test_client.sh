#!/bin/bash -xe

[ $# -ne 3 ] && { echo "Usage $0 : container_name folder_for_junit"  ; exit 1; }

if [ $3 = 'community_svc' ] ; then
    csi_params='csi_params_thin'
else
    csi_params='csi_params'
fi

tests_to_skip_file_path="/usr/local/go/src/github.com/kubernetes-csi/csi-test/ibm-driver/csi_tests_to_skip_$3"

#/tmp/k8s_dir is the directory of the csi grpc\unix socket that shared between csi server and csi-test docker
docker build -f Dockerfile-csi-test --build-arg CSI_PARAMS=${csi_params} --build-arg TESTS_TO_SKIP_FILE_PATH=${tests_to_skip_file_path} -t csi-sanity-test .  && docker run --user=root -e STORAGE_ARRAYS=${STORAGE_ARRAYS} -e USERNAME=${USERNAME} -e PASSWORD=${PASSWORD}  -e POOL_NAME=${POOL_NAME} -v /tmp:/tmp:rw -v$2:/tmp/test_results:rw --rm  --name $1 csi-sanity-test

