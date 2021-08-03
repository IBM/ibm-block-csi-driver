#!/bin/bash -xe
set +o pipefail

install_ci_dependencies (){
  python -m pip install --upgrade pip
  echo docker-hub==2.2.0 > dev-requirements.txt
  pip install -r dev-requirements.txt
}

get_driver_version (){
  source /home/runner/.bash_profile
  cd common
  image_version=`yq eval .identity.version config.yaml`
  cd -
}

install_ci_dependencies
cat >>/home/runner/.bash_profile <<'EOL'
yq() {
  docker run --rm -i -v "${PWD}":/workdir mikefarah/yq "$@"
}
EOL
get_driver_version
GITHUB_SHA=${GITHUB_SHA:0:7}_
driver_image_tags=`scripts/ci/get_image_tags_from_branch.sh ${CI_ACTION_REF_NAME} ${image_version} ${build_number} ${GITHUB_SHA}`
docker_image_branch_tag=`echo $driver_image_tags | awk '{print$2}'`
driver_images_specific_tag=`echo $driver_image_tags | awk '{print$1}'`

if [ "$docker_image_branch_tag" == "develop" ]; then
  docker_image_branch_tag=latest
fi

echo "::set-output name=docker_image_branch_tag::${docker_image_branch_tag}"
echo "::set-output name=driver_images_specific_tag::${driver_images_specific_tag}"
