#!/bin/bash -x

if [ -z "$BANDIT_SKIPS" ]
then
    export BANDIT_SKIPS=""
fi

if [ -z "$GOSEC_EXCLUDE" ]
then
    export GOSEC_EXCLUDE=""
fi


export OUTPUT_PATH="`pwd`/build/reports"

docker build -f Dockerfile-csi-controller-code-scan -t csi-controller-code-scan . && \
docker run --rm -t -v ${OUTPUT_PATH}:/results -e SKIPS=${BANDIT_SKIPS} csi-controller-code-scan

docker build -f Dockerfile-csi-node-code-scan -t csi-node-code-scan . && \
docker run --rm -t -v ${OUTPUT_PATH}:/results -e EXCLUDE=${GOSEC_EXCLUDE} csi-node-code-scan

