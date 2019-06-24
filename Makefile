#
# Copyright 2019 IBM Corp.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#

PKG=github.com/ibm/ibm-block-csi-driver
IMAGE=ibmcom/ibm-block-csi-driver
GIT_COMMIT?=$(shell git rev-parse HEAD)
BUILD_DATE?=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS?="-X ${PKG}/node/pkg/driver.gitCommit=${GIT_COMMIT} -X ${PKG}/node/pkg/driver.buildDate=${BUILD_DATE} -s -w"

GO111MODULE=on
.EXPORT_ALL_VARIABLES:

.PHONY: ibm-block-csi-driver
ibm-block-csi-driver:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux go build -ldflags  ${LDFLAGS} -o bin/ibm-block-csi-node-driver ./node/cmd

.PHONY: test
test:
	go test -v -race ./node/...

.PHONY: gofmt
gofmt:
	gofmt -w .

#.PHONY: image-release
#image-release:
#	docker build -t $(IMAGE):$(VERSION) .
#
#.PHONY: push-release
#push-release:
#	docker push $(IMAGE):$(VERSION)

.PHONY: list
list:
	@$(MAKE) -pRrq -f $(lastword $(MAKEFILE_LIST)) : 2>/dev/null | awk -v RS= -F: '/^# File/,/^# Finished Make data base/ {if ($$1 !~ "^[#.]") {print $$1}}' | sort | egrep -v -e '^[^[:alnum:]]' -e '^$@$$'

