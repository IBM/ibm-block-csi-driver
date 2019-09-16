#!/bin/bash -x

if [ -z "BANDIT_SKIPS" ]
then
    export BANDIT_SKIPS=""
fi

export OUTPUT_PATH="`pwd`/build/reports"

docker build -f Dockerfile-csi-controller-code-scan -t csi-controller-code-scan . && \
docker run --rm -t -v ${OUTPUT_PATH}:/results -e SKIPS=${BANDIT_SKIPS} csi-controller-code-scan

