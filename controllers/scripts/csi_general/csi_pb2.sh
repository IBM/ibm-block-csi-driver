#!/bin/bash -e
set -x

CSI_VERSION="v1.5.0"
ADDONS_VERSION="v0.1.1"
PB2_DIR="csi_general"

mkdir -p ./proto/${PB2_DIR}
cd ./proto/${PB2_DIR}

curl -O https://raw.githubusercontent.com/IBM/csi-volume-group/add/CSI-5164_add_all_spec_with_vg/csi.proto
curl -O https://raw.githubusercontent.com/ELENAGER/spec/from_tag_v0.1.1/replication.proto

cd -

python -m grpc_tools.protoc --proto_path=proto \
                            --python_out=. \
                            --grpc_python_out=. \
                            proto/${PB2_DIR}/*.proto

rm -rf ./proto/
touch ${PB2_DIR}/__init__.py
