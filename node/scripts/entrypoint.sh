#! /bin/bash -x

echo "Install iscsid..."
yum install -y iscsi-initiator-utils && yum clean all

echo "Starting iscsid..."
iscsid -f &

# sleep one second to make sure iscsid is running in background
sleep 1

echo "Starting driver with arguments $@..."
/root/ibm-block-csi-node-driver $@
