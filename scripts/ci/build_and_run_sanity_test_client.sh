#!/bin/bash -xe

#docker build  -f Dockerfile-csi-test -t csi-sanity-test . && docker run  --rm  --name $1 csi-sanity-test

echo "@@@###@@@ ${MGMT_ADDRESS} ${USERNAME} ${PASSWORD}"

docker build --build-arg MGMT_ADDRESS=${MGMT_ADDRESS} --build-arg USERNAME=${USERNAME} --build-arg PASSWOR=${PASSWORD}  -f Dockerfile-csi-test -t csi-sanity-test . && docker run -v /tmp/k8s_dir:/tmp/k8s_dir:rw --rm  --name $1 csi-sanity-test -e
