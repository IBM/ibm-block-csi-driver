#!/bin/bash -x
docker run -t -v `pwd`/deploy/kubernetes/v1.13:/deploy/kubernetes/v1.13 garethr/kubeval deploy/kubernetes/v1.13/*.yaml
