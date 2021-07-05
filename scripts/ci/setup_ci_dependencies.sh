#!/bin/bash -xe
set +o pipefail

python -m pip install --upgrade pip docker-hub==2.2.0
echo docker-hub > dev-requirements.txt

cat >>/home/runner/.bash_profile <<'EOL'
yq() {
  docker run --rm -e operator_image_for_test=$operator_image_for_test\
                  -e controller_repository_for_test=$controller_repository_for_test\
                  -e node_repository_for_test=$node_repository_for_test\
                  -e driver_images_tag=$driver_images_tag\
                  -i -v "${PWD}":/workdir mikefarah/yq "$@"
}
EOL
