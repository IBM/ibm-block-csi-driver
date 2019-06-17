#!/bin/bash -xe

./scripts/ci/run_controller_server_for_csi_test.sh csi-controller > csi_controller_logs 2>&1 &
echo `pwd`
sleep 2
mkdir -p build/reports && chmod 777 build/reports
./scripts/ci/run_csi_test_client.sh csi-sanity-test `pwd`/build/reports/