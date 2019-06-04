#!/bin/bash -xe

docker build  -f Dockerfile-csi-test -t csi-sanity-test . && docker run  --rm  --name $1 csi-sanity-test
