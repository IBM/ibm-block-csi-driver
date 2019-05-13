#!/bin/bash -x

[ -n "$1" ] && coverage="-v $1:/driver/coverage"
docker build -f Dockerfile-csi-controller.test -t csi-controller-unitests . && docker run --rm -t $coverage csi-controller-unitests
