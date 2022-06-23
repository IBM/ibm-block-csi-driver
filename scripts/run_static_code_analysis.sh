#!/bin/bash
set -x

docker build -f Dockerfile-csi-controller.test -t csi-controller-tests . || exit 1

EXIT_STATUS=0
docker run --entrypoint ./controllers/scripts/pycodestyle.sh --rm csi-controller-tests || EXIT_STATUS=$?
docker run --entrypoint ./controllers/scripts/pylint.sh --rm csi-controller-tests || EXIT_STATUS=$?
exit ${EXIT_STATUS}
