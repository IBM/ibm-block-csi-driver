#!/bin/bash -xe
set +o pipefail

echo $'yq() {\n  docker run --rm -e operator_image_for_test=$operator_image_for_test -e controller_repository_for_test=$controller_repository_for_test -e node_repository_for_test=$node_repository_for_test -e driver_images_tag=$driver_images_tag -i -v "${PWD}":/workdir mikefarah/yq:4 "$@"\n}' >> /home/runner/.bash_profile
