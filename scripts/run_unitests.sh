#!/bin/bash -x

[ -n "$1" ] && coverage="-v $1:/driver/coverage:z"
docker build -f Dockerfile-csi-controller.test -t csi-controller-tests . && \
docker run --entrypoint ./controller/scripts/unitests.sh --rm -t $coverage csi-controller-tests
