#!/bin/bash -xe
IMAGE_VERSION=$1
BUILD_NUMBER=$2
GIT_BRANCH=$3
branch=`echo $GIT_BRANCH| sed 's|/|.|g'`  #not sure if docker accept / in the version
specific_tag="${IMAGE_VERSION}_b${BUILD_NUMBER}_${branch}"
echo $specific_tag
echo $branch
