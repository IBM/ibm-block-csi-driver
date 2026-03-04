#!/bin/bash
set -x
[ -n "$1" ] && coverage="-v $1:/driver/coverage:z"
podman build -f Dockerfile-controllers.test -t csi-controller-tests . && \
