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
cd -

driver_images_tag=`scripts/ci/get_image_tags_from_branch.sh ${image_version} ${build_number} ${CI_ACTION_REF_NAME}`
docker_image_branch_tag=`echo $driver_images_tag | awk '{print$2}'`
driver_images_tag=`echo $driver_images_tag | awk '{print$1}'`
echo "::set-output name=docker_image_branch_tag::${docker_image_branch_tag}"
echo "::set-output name=driver_images_tag::${driver_images_tag}"
