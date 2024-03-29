#!/bin/bash -ex

echo "update CSI secret"
sed -i -e "s/STORAGE_ARRAYS/${STORAGE_ARRAYS}/g" ${SECRET_FILE}
sed -i -e "s/USERNAME/${USERNAME}/g" ${SECRET_FILE}
sed -i -e "s/PASSWORD/${PASSWORD}/g" ${SECRET_FILE}

echo "update params file"
sed -i -e "s/POOL_NAME/${POOL_NAME}/g" ${PARAM_FILE}
sed -i -e "s/VIRT_SNAP_FUNC/${VIRT_SNAP_FUNC}/g" ${SNAPSHOT_PARAM_FILE}
sed -i -e "s/POOL_NAME/${POOL_NAME}/g" ${SNAPSHOT_PARAM_FILE}

# get tests to skip
TESTS=`cat ${TESTS_TO_SKIP_FILE}| sed -Ez '$ s/\n+$//' | tr '\n' "|"`

/usr/local/go/src/github.com/kubernetes-csi/csi-test/cmd/csi-sanity/csi-sanity \
--csi.endpoint ${ENDPOINT} \
--csi.controllerendpoint ${ENDPOINT_CONTROLLER} \
--csi.secrets ${SECRET_FILE} \
--csi.testvolumeparameters ${PARAM_FILE} \
--csi.testsnapshotparameters ${SNAPSHOT_PARAM_FILE} \
--csi.junitfile ${JUNIT_OUTPUT} \
--ginkgo.v \
--ginkgo.debug \
--ginkgo.skip "${TESTS}"

