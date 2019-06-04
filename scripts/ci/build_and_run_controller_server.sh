#!/bin/bash -xe

docker build  -f Dockerfile-csi-controller csi-controller . &&  docker run  --name $1  csi-controller -e [::]:4444

