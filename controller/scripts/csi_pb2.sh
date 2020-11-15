#!/bin/bash
set -x

version="v1.2.0"
curl -o csi.proto https://raw.githubusercontent.com/container-storage-interface/spec/"$version"/csi.proto
python -m grpc_tools.protoc --proto_path=. --grpc_python_out=./controller/csi_general --python_out=./controller/csi_general csi.proto
