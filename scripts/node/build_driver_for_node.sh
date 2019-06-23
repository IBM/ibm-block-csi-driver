#!/bin/bash

set -e

scripts=$(dirname $0)

echo "Building csi node driver"
go build -ldflags '-w -linkmode external -extldflags "-static"' -o  $scripts/../../bin/ibm-block-csi-node-driver $scripts/../../node/cmd/main.go

