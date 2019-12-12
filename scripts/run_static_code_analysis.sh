#!/bin/bash -x

docker build -f Dockerfile-csi-controller.test -t csi-controller-tests . && \
docker run --entrypoint ./controller/scripts/pycodestyle.sh --rm csi-controller-tests
docker run --entrypoint ./controller/scripts/pylint.sh --rm csi-controller-tests
