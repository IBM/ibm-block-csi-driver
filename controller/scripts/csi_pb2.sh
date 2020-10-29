#!/bin/bash
set -x

version="$1" # currently k8s on v1.2.0
curl -o csi.proto https://raw.githubusercontent.com/container-storage-interface/spec/"$version"/csi.proto
python -m grpc_tools.protoc --proto_path=. --grpc_python_out=./controller/csi_general --python_out=./controller/csi_general csi.proto