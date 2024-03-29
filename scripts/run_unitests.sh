#!/bin/bash
set -x
[ -n "$1" ] && coverage="-v $1:/driver/coverage:z"
docker build -f Dockerfile-controllers.test -t csi-controller-tests . && \
docker run --entrypoint ./controllers/scripts/unitests.sh --rm -t $coverage csi-controller-tests
