#!/bin/bash


echo "usr/local/go/src/github.com/kubernetes-csi/csi-test/cmd/csi-sanity/csi-sanity  --csi.endpoint $1 --csi.secrets $2 --csi.testvolumeparameters $3 --ginkgo.focus $4"

# update CSI secret
sed -i -e "s/MGMT_ADDRESS/${MGMT_ADDRESS}/g" $2
sed -i -e "s/USERNAME/${USERNAME}/g" $2
sed -i -e "s/PASSWORD/${PASSWORD}/g" $2

echo "####### ${MGMT_ADDRESS} ${USERNAME} ${PASSWORD} "
cat $2

#sed -i -e 's/MGMT_ADDRESS/9.151.156.113/g' $2

/usr/local/go/src/github.com/kubernetes-csi/csi-test/cmd/csi-sanity/csi-sanity  --csi.endpoint $1 --csi.secrets $2 --csi.testvolumeparameters $3  --csi.junitfile $4 --ginkgo.focus ${TESTS_TO_RUN}



