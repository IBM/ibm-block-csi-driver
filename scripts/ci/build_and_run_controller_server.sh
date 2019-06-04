#!/bin/bash -xe

docker build  -f Dockerfile-csi-controller -t csi-controller . &&  docker run  --name $1  csi-controller -e [::]:4444

