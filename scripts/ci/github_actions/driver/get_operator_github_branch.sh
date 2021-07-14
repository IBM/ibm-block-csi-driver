#!/bin/bash -xe
set +o pipefail

is_operator_branch_image_exists=false
operator_branch=develop
DOCKER_HUB_USERNAME=csiblock1
DOCKER_HUB_PASSWORD=$csiblock_dockerhub_password
triggering_branch=$CI_ACTION_REF_NAME
target_image_tag=`echo $triggering_branch | sed 's|/|.|g'`
export image_tags=`docker-hub tags --orgname csiblock1 --reponame ibm-block-csi-operator --all-pages | grep $target_image_tag | awk '{print$2}'`

for tag in $image_tags
do
  if [[ "$tag" == "$target_image_tag" ]]; then
    is_operator_branch_image_exists=true
  fi
done

if [ $is_operator_branch_image_exists == "true" ]; then
  operator_branches=`curl -H "Authorization: token $github_token" https://api.github.com/repos/IBM/ibm-block-csi-operator/branches | jq -c '.[]' | jq -r .name`
  for branch_name in $operator_branches
  do
    if [ "$branch_name" == "$triggering_branch" ]; then
      operator_branch=$triggering_branch
    fi
  
  done
fi

docker_image_branch_tag=`echo $operator_branch| sed 's|/|.|g'`
echo "::set-output name=docker_image_branch_tag::${docker_image_branch_tag}"
echo "::set-output name=operator_branch::${operator_branch}"
