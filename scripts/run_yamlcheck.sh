#!/bin/bash -xe
docker run -t -v `pwd`/deploy/kubernetes/v1.13:/deploy/kubernetes/v1.13 garethr/kubeval deploy/kubernetes/v1.13/*.yaml
docker run -t -v `pwd`/deploy/kubernetes/v1.14:/deploy/kubernetes/v1.14 garethr/kubeval deploy/kubernetes/v1.14/*.yaml

