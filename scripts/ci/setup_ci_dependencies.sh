#!/bin/bash -xe
set +o pipefail

echo $'yq() {\n  docker run --rm -e operator_image_for_test=$operator_image_for_test -i -v "${PWD}":/workdir mikefarah/yq:4 "$@"\n}' >> /home/runner/.bash_profile
