#!/bin/bash -e
set -x

CSI_VERSION="v1.2.0"
ADDONS_VERSION="v0.2.0"
PB2_DIR="controller/csi_general"

mkdir -p ./proto/${PB2_DIR}
cd ./proto/${PB2_DIR}

curl -O https://raw.githubusercontent.com/container-storage-interface/spec/${CSI_VERSION}/csi.proto
curl -O https://raw.githubusercontent.com/csi-addons/spec/${ADDONS_VERSION}/replication.proto

cd -

python -m grpc_tools.protoc --proto_path=proto \
                            --python_out=. \
                            --grpc_python_out=. \
                            proto/${PB2_DIR}/*.proto

rm -rf ./proto/${PB2_DIR}
