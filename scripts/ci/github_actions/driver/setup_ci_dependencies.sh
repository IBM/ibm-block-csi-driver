#!/bin/bash -xe
set +o pipefail

python -m pip install --upgrade pip
echo docker-hub==2.2.0 > dev-requirements.txt
pip install -r dev-requirements.txt

source /home/runner/.bash_profile
cd common
image_version=`yq eval .identity.version config.yaml`
cd -

GITHUB_SHA=${GITHUB_SHA:0:7}_
driver_image_tags=`scripts/ci/get_image_tags_from_branch.sh ${image_version} ${build_number} ${CI_ACTION_REF_NAME} ${GITHUB_SHA}`
docker_image_branch_tag=`echo $driver_image_tags | awk '{print$2}'`
driver_images_specific_tag=`echo $driver_image_tags | awk '{print$1}'`

if [ "$docker_image_branch_tag" == "develop" ]; then
  docker_image_branch_tag=latest
fi

echo "::set-output name=docker_image_branch_tag::${docker_image_branch_tag}"
echo "::set-output name=driver_images_specific_tag::${driver_images_specific_tag}"
