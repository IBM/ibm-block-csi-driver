#!/bin/bash -xe
GIT_BRANCH=$1
IMAGE_VERSION=$2
BUILD_NUMBER=$3
COMMIT_HASH=${4:0:7}
branch_tag=$(echo $GIT_BRANCH| sed 's|/|.|g')  #not sure if docker accept / in the version
specific_tag="${IMAGE_VERSION}_b${BUILD_NUMBER}_${COMMIT_HASH}_${branch_tag}"
echo $specific_tag
echo $branch_tag
