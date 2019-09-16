#!/bin/bash -x

export SKIPS="B703"
export OUTPUT_PATH="`pwd`/build/reports"

docker build -f Dockerfile-csi-controller-code-scan -t csi-controller-code-scan . && \
docker run --rm -t -v ${OUTPUT_PATH}:/results -e SKIPS=${SKIPS} csi-controller-code-scan

