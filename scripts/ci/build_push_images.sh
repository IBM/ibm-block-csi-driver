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
[ "$GIT_BRANCH" = "develop" -o "$GIT_BRANCH" = "origin/develop" -o "$GIT_BRANCH" = "master" ] && is_tag_latest="true" || is_tag_latest="false"

images_file=$1
[ -n "$images_file" ] && printf "" > $images_file || :

# Setup pip cache volume (same as used in test stages)
ARCH=$(uname -m)
VOLUME_NAME="pip-cache-${ARCH}"

# Detect container runtime
if command -v podman &> /dev/null; then
    CONTAINER_CMD="podman"
elif command -v docker &> /dev/null; then
    CONTAINER_CMD="docker"
else
    echo "Error: Neither podman nor docker found"
    exit 1
fi

# Create persistent volume if it doesn't exist
if [ "${CONTAINER_CMD}" = "podman" ]; then
    ${CONTAINER_CMD} volume create ${VOLUME_NAME} 2>/dev/null || true
else
    if ! ${CONTAINER_CMD} volume inspect ${VOLUME_NAME} &> /dev/null; then
        ${CONTAINER_CMD} volume create ${VOLUME_NAME}
    fi
fi

echo "Using pip cache volume: ${VOLUME_NAME}"

build_and_push (){
    repository=$1
    dockerfile=$2
    driver_type=$3
    registry="${DOCKER_REGISTRY}/${repository}"
    tag_specific="${registry}:${specific_tag}"
    tag_latest="${registry}:latest"
    [ "$is_tag_latest" = "true" ] && taglatestflag="-t ${tag_latest}"

    echo "Build and push ${driver_type} image"
    # Use volume mount for pip cache to speed up builds on non-x86 platforms
    # Mount at /opt/app-root/.cache/pip (pip cache location for uid 1001)
    docker build --volume ${VOLUME_NAME}:/opt/app-root/.cache/pip \
        -t $tag_specific $taglatestflag \
        -f $dockerfile \
        --build-arg VERSION="${IMAGE_VERSION}" \
        --build-arg BUILD_NUMBER="${BUILD_NUMBER}" \
        .
    docker push $tag_specific
    [ "$is_tag_latest" = "true" ] && docker push $tag_latest || :
    [ -n "$images_file" ] && printf "${tag_specific}\n" >> $images_file || :
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
