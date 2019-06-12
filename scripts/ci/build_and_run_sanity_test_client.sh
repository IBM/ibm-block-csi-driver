#!/bin/bash -xe

#docker build  -f Dockerfile-csi-test -t csi-sanity-test . && docker run  --rm  --name $1 csi-sanity-test


echo "@@@###@@@ ${MGMT_ADDRESS} ${USERNAME} ${PASSWORD} ${TESTS_TO_RUN} "

docker build  --build-arg MGMT_ADDRESS=${MGMT_ADDRESS} --build-arg USERNAME=${USERNAME} --build-arg PASSWORD=${PASSWORD} --build-arg TESTS_TO_RUN=${TESTS_TO_RUN}  -f Dockerfile-csi-test -t csi-sanity-test . && docker run -v /tmp/k8s_dir:/tmp/k8s_dir:rw  -v$2:/tmp/test_results:rw --rm  --name $1 csi-sanity-test
