#!/bin/bash -xe

[ -z "${CONTROLLER_LOGS}" ] && { echo "CONTROLLER_LOGS env is mandatory"; exit 1; }
# assume that all the environment storage was setup in advance.
echo "controller logs : ${CONTROLLER_LOGS}"
./scripts/ci/run_controller_server_for_csi_test.sh csi-controller > ${CONTROLLER_LOGS} 2>&1
./scripts/ci/run_node_server_for_csi_test.sh csi-node > "${CONTROLLER_LOGS}_node" 2>&1
echo `pwd`
sleep 2
mkdir -p build/reports && chmod 777 build/reports
set +e
./scripts/ci/run_csi_test_client.sh csi-sanity-test `pwd`/build/reports/

docker logs csi-controller > "${CONTROLLER_LOGS}_controller_run.log"
docker logs csi-node > "${CONTROLLER_LOGS}_node_run.log"
docker kill csi-controller