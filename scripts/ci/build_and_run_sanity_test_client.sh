#!/bin/bash -xe

#docker build  -f Dockerfile-csi-test -t csi-sanity-test . && docker run  --rm  --name $1 csi-sanity-test

docker build  -f Dockerfile-csi-test -t csi-sanity-test . && docker run -v /tmp/k8s_dir:/tmp/k8s_dir:rw --rm  --name $1 csi-sanity-test
