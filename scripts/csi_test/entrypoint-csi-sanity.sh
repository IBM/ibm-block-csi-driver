#!/bin/bash

#### PARAMETERS
# $1 - endpoint
# $2 - secret
# $3 - parameter file
# $4 - junit output
# $5 - tests to run file

# update CSI secret
sed -i -e "s/MGMT_ADDRESS/${MGMT_ADDRESS}/g" $2
sed -i -e "s/USERNAME/${USERNAME}/g" $2
sed -i -e "s/PASSWORD/${PASSWORD}/g" $2

# update params file
sed -i -e "s/POOL_NAME/${POOL_NAME}/g" $3

# get tests to run
test=`cat $5| tr '\n' "|"`

echo "JUNIT OUTPUT"
echo ${JUNIT_OUTPUT}

/usr/local/go/src/github.com/kubernetes-csi/csi-test/cmd/csi-sanity/csi-sanity  --csi.endpoint $1 --csi.secrets $2 --csi.testvolumeparameters $3  --csi.junitfile $4 --ginkgo.focus ${tests}
