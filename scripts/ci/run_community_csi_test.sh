#!/bin/bash -xe

# assume that all the environment storage was setup in advance.
./scripts/ci/run_controller_server_for_csi_test.sh csi-controller > "csi_controller_deploy.log" 2>&1
./scripts/ci/run_node_server_for_csi_test.sh csi-node > "csi_node_deploy.log" 2>&1
echo `pwd`
sleep 2
mkdir -p build/reports && chmod 777 build/reports
set +e
./scripts/ci/run_csi_test_client.sh csi-sanity-test `pwd`/build/reports/ $1

docker logs csi-controller >& "csi_controller_run.log"
docker logs csi-node >& "csi_node_run.log"
docker kill csi-controller
docker kill csi-node
docker rm csi-controller
docker rm csi-node
