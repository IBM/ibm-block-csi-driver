#!/bin/bash -x

TARGET_NAME=$1
OUTPUT_PATH="`pwd`/build/reports"

docker build -f scripts/ci/code_scanning/Dockerfile-${TARGET_NAME} -t ${TARGET_NAME} . && \
docker run --rm -t -v ${OUTPUT_PATH}:/results ${TARGET_NAME}