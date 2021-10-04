#!/bin/bash -xe
set +o pipefail

install_ci_dependencies (){
  scripts/ci/github_actions/setup_yq.sh
  python -m pip install --upgrade pip==21.2.4
  echo docker-hub==2.2.0 > dev-requirements.txt
  pip install -r dev-requirements.txt
  source /home/runner/.bash_profile
}

get_driver_version (){
  yq eval .identity.version common/config.yaml
}

install_ci_dependencies
driver_version=$(get_driver_version)
GITHUB_SHA=${GITHUB_SHA:0:7}_
driver_image_tags=`scripts/ci/get_image_tags_from_branch.sh ${CI_ACTION_REF_NAME} ${driver_version} ${build_number} ${GITHUB_SHA}`
docker_image_branch_tag=`echo $driver_image_tags | awk '{print$2}'`
driver_images_specific_tag=`echo $driver_image_tags | awk '{print$1}'`

if [ "$docker_image_branch_tag" == "develop" ]; then
  docker_image_branch_tag=latest
fi

echo "::set-output name=docker_image_branch_tag::${docker_image_branch_tag}"
echo "::set-output name=driver_images_specific_tag::${driver_images_specific_tag}"
