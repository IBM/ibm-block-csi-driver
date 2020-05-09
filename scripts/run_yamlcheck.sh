#!/bin/bash -xe

arch=$(uname -m)
if [[ "$arch" == "x86"* ]];
then
docker run -t -v `pwd`/deploy/kubernetes/v1.13:/deploy/kubernetes/v1.13 garethr/kubeval deploy/kubernetes/v1.13/*.yaml
docker run -t -v `pwd`/deploy/kubernetes/v1.14:/deploy/kubernetes/v1.14 garethr/kubeval deploy/kubernetes/v1.14/*.yaml
else
echo "Skipping yaml validations stage. garethr/kubeval only supports x86"
fi
