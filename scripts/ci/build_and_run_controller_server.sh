#!/bin/bash -xe

docker build  -f Dockerfile-csi-controller csi-controller $1 . &&  docker run  --name $1  csi-controller -e [::]:4444

