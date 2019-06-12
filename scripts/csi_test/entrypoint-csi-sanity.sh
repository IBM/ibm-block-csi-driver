#!/bin/bash

#### PARAMETERS
# $1 - endpoint
# $2 - secret
# $3 - parameter file
# $4 - junit output
# $5 - tests to run file

echo "JUNIT OUTPUT"
echo ${JUNIT_OUTPUT} ${SECRET_FILE} ${PARAM_FILE} ${ENDPOINT} ${TESTS_TO_RUN_FILE}

# update CSI secret
sed -i -e "s/MGMT_ADDRESS/${MGMT_ADDRESS}/g" ${SECRET_FILE}
sed -i -e "s/USERNAME/${USERNAME}/g" ${SECRET_FILE}
sed -i -e "s/PASSWORD/${PASSWORD}/g" ${SECRET_FILE}

# update params file
sed -i -e "s/POOL_NAME/${POOL_NAME}/g" ${PARAM_FILE}

# get tests to run
TESTS=`cat ${TESTS_TO_RUN_FILE}| tr '\n' "|"`


/usr/local/go/src/github.com/kubernetes-csi/csi-test/cmd/csi-sanity/csi-sanity  --csi.endpoint ${ENDPOINT} --csi.secrets ${SECRET_FILE} --csi.testvolumeparameters ${PARAM_FILE}  --csi.junitfile ${JUNIT_OUTPUT} --ginkgo.focus ${TESTS}
