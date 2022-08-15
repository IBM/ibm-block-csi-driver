#!/bin/bash -xe

# Validations
MANDATORY_ENVS="IMAGE_VERSION BUILD_NUMBER DOCKER_REGISTRY CSI_NODE_IMAGE CSI_CONTROLLER_IMAGE CSI_HOST_DEFINER_IMAGE GIT_BRANCH"
for envi in $MANDATORY_ENVS; do 
    [ -z "${!envi}" ] && { echo "Error - Env $envi is mandatory for the script."; exit 1; } || :
done

# Prepare specific tag for the image
branch=`echo $GIT_BRANCH| sed 's|/|.|g'`  #not sure if docker accept / in the version
specific_tag="${IMAGE_VERSION}_b${BUILD_NUMBER}_${branch}"

# Set latest tag only if its from develop branch or master and prepare tags
[ "$GIT_BRANCH" = "develop" -o "$GIT_BRANCH" = "origin/develop" -o "$GIT_BRANCH" = "master" ] && tag_latest="true" || tag_latest="false"
[ -n "$1" ] && printf "" > $1 || :

build_and_push (){
    repository=$1
    dockerfile=$2
    driver_type=$3
    registry="${DOCKER_REGISTRY}/${repository}"
    tag_specific="${registry}:${specific_tag}"
    tag_latest="${registry}:latest"
    [ "$tag_latest" = "true" ] && taglatestflag="-t ${tag_latest}"

    echo "Build and push ${driver_type} image"
    docker build -t $tag_specific $taglatestflag -f $dockerfile --build-arg VERSION="${IMAGE_VERSION}" --build-arg BUILD_NUMBER="${BUILD_NUMBER}" .
    docker push $tag_specific
    [ "$tag_latest" = "true" ] && docker push $tag_latest || :
    [ -n "$1" ] && printf "${tag_specific}\n" >> $1 || :
    echo ""
    echo "Image ready:"
    echo "   ${tag_specific}"
}

# CSI controller
# --------------
build_and_push $CSI_CONTROLLER_IMAGE Dockerfile-csi-controller controller

# CSI node
# --------
build_and_push $CSI_NODE_IMAGE Dockerfile-csi-node node

# Host Definer
# --------
build_and_push $CSI_HOST_DEFINER_IMAGE Dockerfile-csi-host-definer "Host Definer"
