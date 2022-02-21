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

# Note: this Makefile currently applicable responsible for CSI compile, test and build image. Later will should add csi-controller to the makefile party.

PKG=github.com/ibm/ibm-block-csi-driver
IMAGE=ibmcom/ibm-block-csi-driver
GIT_COMMIT?=$(shell git rev-parse HEAD)
BUILD_DATE?=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS?="-X ${PKG}/node/pkg/driver.gitCommit=${GIT_COMMIT} -X ${PKG}/node/pkg/driver.buildDate=${BUILD_DATE} -s -w"
GO111MODULE=on
DRIVER_CONFIG_YML=$(shell pwd)/common/config.yaml
# -race is not supported on Z
GO_TEST_FLAGS=$(shell if [ "$$(uname -m)" = "s390x" ]; then echo "-v"; else echo "-v -race"; fi)

.EXPORT_ALL_VARIABLES:

define gofmt-test =
	@echo ">> checking code style"
	@fmtRes=$$(gofmt -d $$(find ./node/ -name '*.go')); \
	if [ -n "$${fmtRes}" ]; then \
		echo "gofmt checking failed!"; echo "$${fmtRes}"; echo; \
		exit 1; \
	fi
@echo ">> code style passed!"
endef


.PHONY: ibm-block-csi-driver
ibm-block-csi-driver:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux go build -ldflags  ${LDFLAGS} -o bin/ibm-block-csi-node-driver ./node/cmd

.PHONY: test
test:
	if [ -d ./node/mocks ]; then rm -rf ./node/mocks; fi
	go generate ./...
	$(gofmt-test)
	go vet -c=1 ./node/...
	go test ${GO_TEST_FLAGS} ./node/...

.PHONY: test-xunit
test-xunit:
	mkdir -p ./build/reports
	if [ -d ./node/mocks ]; then rm -rf ./node/mocks; fi
	go generate ./...
	$(gofmt-test)
	go vet -c=1 ./node/...
	go test ${GO_TEST_FLAGS} ./node/... | go2xunit -output build/reports/csi-node-unitests.xml
	go test ${GO_TEST_FLAGS} ./node/...	# run again so the makefile will fail in case tests failing

.PHONY: test-xunit-in-container
test-xunit-in-container:
    # Run make test-xunit inside csi node container for testing (to avoid go and other testing utils on your laptop).
	docker build -f Dockerfile-csi-node.test -t csi-node-unitests .
	docker run --rm -t -v $(CURDIR)/build/reports/:/go/src/github.com/ibm/ibm-block-csi-driver/build/reports/ csi-node-unitests


.PHONY: gofmt
gofmt:
	gofmt -w ./node

.PHONY: csi-build-images-and-push-artifactory
csi-build-images-and-push-artifactory:
	./scripts/ci/build_push_images.sh

.PHONY: list
list:
	@$(MAKE) -pRrq -f $(lastword $(MAKEFILE_LIST)) : 2>/dev/null | awk -v RS= -F: '/^# File/,/^# Finished Make data base/ {if ($$1 !~ "^[#.]") {print $$1}}' | sort | egrep -v -e '^[^[:alnum:]]' -e '^$@$$'

