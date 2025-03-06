#!/bin/bash -e
set -x

CSI_VERSION="v1.9.0"
ADDONS_VERSION="v0.2.0"
VG_VERSION="v0.9.2"
PB2_DIR="csi_general"

mkdir -p ./proto/${PB2_DIR}
cd ./proto/${PB2_DIR}

curl -O https://raw.githubusercontent.com/container-storage-interface/spec/${CSI_VERSION}/csi.proto
curl -O https://raw.githubusercontent.com/IBM/csi-volume-group/${VG_VERSION}/volumegroup/volumegroup.proto
curl -O https://raw.githubusercontent.com/csi-addons/spec/${ADDONS_VERSION}/replication/replication.proto
sed -i 's|github.com/container-storage-interface/spec/lib/go/csi/csi.proto|csi_general/csi.proto|g' replication.proto
cd -

python -m grpc_tools.protoc --proto_path=proto \
                            --python_out=. \
                            --grpc_python_out=. \
                            proto/${PB2_DIR}/*.proto

rm -rf ./proto/
touch ${PB2_DIR}/__init__.py
