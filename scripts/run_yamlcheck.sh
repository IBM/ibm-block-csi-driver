#!/bin/bash -x
docker run -t -v `pwd`/deploy/kubernetes/v1.14:/deploy/kubernetes/v1.14 garethr/kubeval deploy/kubernetes/v1.14/*.yaml
