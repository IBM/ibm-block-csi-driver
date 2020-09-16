#!/bin/bash -xe

arch=$(uname -m)
if [[ "$arch" == "x86"* ]];
then
docker run -t -v `pwd`/deploy/kubernetes/examples:/deploy/kubernetes/examples garethr/kubeval --ignore-missing-schemas deploy/kubernetes/examples/*.yaml
else
echo "Skipping yaml validations stage. garethr/kubeval only supports x86"
fi
