#!/bin/bash -x

[ -n "$1" ] && coverage="-v $1:/coverage"
docker build -f Dockerfile.test -t csi-controller-unitests . && docker run --rm -it $coverage csi-controller-unitests
