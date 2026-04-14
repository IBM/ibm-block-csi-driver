#!/bin/bash
set -x

# Enable Docker BuildKit for cache mount support
export DOCKER_BUILDKIT=1

docker build -f Dockerfile-controllers.test -t csi-controller-tests . || exit 1

EXIT_STATUS=0
docker run --entrypoint ./controllers/scripts/pycodestyle.sh --rm csi-controller-tests || EXIT_STATUS=$?
docker run --entrypoint ./controllers/scripts/pylint.sh --rm csi-controller-tests || EXIT_STATUS=$?
exit ${EXIT_STATUS}
