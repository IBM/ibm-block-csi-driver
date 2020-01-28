#!/bin/bash -x

TARGET_NAME=$1
DOCKERFILE_PATH=$2
OUTPUT_PATH="`pwd`/build/reports/${TARGET_NAME}"
BASELINE_PATH="`pwd`/build/code_scan/${TARGET_NAME}"

mkdir ${BASELINE_PATH} && cp -r /root/code_scan/${TARGET_NAME}/* ${BASELINE_PATH}

docker build -f ${DOCKERFILE_PATH}/Dockerfile-${TARGET_NAME} -t ${TARGET_NAME} . && \
docker run -e TARGET_NAME=${TARGET_NAME} --rm -t -v ${OUTPUT_PATH}:/results ${TARGET_NAME}

docker build -f ${DOCKERFILE_PATH}/Dockerfile-csi-code-scan-results -t csi-code-scan-results . && \
docker run -e TARGET_NAME=${TARGET_NAME} --rm -t -v ${OUTPUT_PATH}:/results -v ${BASELINE_PATH}:/baseline_results csi-code-scan-results
