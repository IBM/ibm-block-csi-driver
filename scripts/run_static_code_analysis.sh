#!/bin/bash
set -x

podman build -f Dockerfile-controllers.test -t csi-controller-tests . || exit 1

EXIT_STATUS=0
podman run --entrypoint ./controllers/scripts/pycodestyle.sh --rm csi-controller-tests || EXIT_STATUS=$?
podman run --entrypoint ./controllers/scripts/pylint.sh --rm csi-controller-tests || EXIT_STATUS=$?
exit ${EXIT_STATUS}
