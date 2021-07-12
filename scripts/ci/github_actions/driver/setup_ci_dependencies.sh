#!/bin/bash -xe
set +o pipefail

python -m pip install --upgrade pip docker-hub==2.2.0
echo docker-hub > dev-requirements.txt

cat >>/home/runner/.bash_profile <<'EOL'
yq() {
  docker run --rm -e operator_image_for_test=$operator_image_for_test\
                  -e cr_image_value=$cr_image_value\
                  -i -v "${PWD}":/workdir mikefarah/yq "$@"
}
EOL

source /home/runner/.bash_profile
cd common
image_version=`yq eval .identity.version config.yaml`
echo "::set-output name=image_version::${image_version}"
