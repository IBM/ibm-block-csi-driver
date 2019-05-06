#!/bin/bash -x

[ -n "$1" ] && coverage="-v $1:/driver/coverage"
docker build -f Dockerfile.test -t csi-controller-unitests . && docker run --rm -it $coverage csi-controller-unitests
