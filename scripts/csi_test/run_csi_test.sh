#!/bin/bash

echo "usr/local/go/src/github.com/kubernetes-csi/csi-test/cmd/csi-sanity/csi-sanity  --csi.endpoint $1 --csi.secrets $2 --csi.testvolumeparameters $3 --ginkgo.focus $4"
/usr/local/go/src/github.com/kubernetes-csi/csi-test/cmd/csi-sanity/csi-sanity  --csi.endpoint $1 --csi.secrets $2 --csi.testvolumeparameters $3 --ginkgo.focus $4



