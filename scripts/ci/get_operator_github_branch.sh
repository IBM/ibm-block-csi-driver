#!/bin/bash -xe
set +o pipefail

does_operator_branch_has_image=false
operator_branch=develop
DOCKER_HUB_USERNAME=csiblock1
DOCKER_HUB_PASSWORD=$csiblock_dockerhub_password
wanted_image_tag=`echo $CI_ACTION_REF_NAME | sed 's|/|.|g'`
export image_tags=`docker-hub tags --orgname csiblock1 --reponame ibm-block-csi-operator --all-pages | grep $wanted_image_tag | awk '{print$2}'`

for tag in $image_tags
do
  if [[ "$tag" == "$wanted_image_tag" ]]; then
    does_operator_branch_has_image=true
  fi
done

if [ $does_operator_branch_has_image == "true" ]; then
  operator_branches=`curl -H "Authorization: token $github_token" https://api.github.com/repos/IBM/ibm-block-csi-operator/branches | jq -c '.[]' | jq -r .name`
  for branch_name in $operator_branches
  do
    if [ "$branch_name" == "$CI_ACTION_REF_NAME" ]; then
      operator_branch=$CI_ACTION_REF_NAME
    fi
  
  done
fi

docker_image_branch_tag=`echo $operator_branch| sed 's|/|.|g'`
echo "::set-output name=docker_image_branch_tag::${docker_image_branch_tag}"
echo "::set-output name=operator_branch::${operator_branch}"
