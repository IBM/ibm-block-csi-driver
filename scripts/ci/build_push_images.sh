#!/bin/bash -xe

# Validations
MANDATORY_ENVS="IMAGE_VERSION BUILD_NUMBER DOCKER_REGISTRY CSI_NODE_IMAGE CSI_CONTROLLER_IMAGE GIT_BRANCH"
for envi in $MANDATORY_ENVS; do 
    [ -z "${!envi}" ] && { echo "Error - Env $envi is mandatory for the script."; exit 1; } || :
done

# Prepare specific tag for the image
branch=`echo $GIT_BRANCH| sed 's|/|.|g'`  #not sure if docker accept / in the version
specific_tag="${IMAGE_VERSION}_b${BUILD_NUMBER}_${branch}"

# Set latest tag only if its from develop branch or master and prepare tags
[ "$GIT_BRANCH" = "develop" -o "$GIT_BRANCH" = "origin/develop" -o "$GIT_BRANCH" = "master" ] && tag_latest="true" || tag_latest="false"


# CSI controller
# --------------
ctl_registry="${DOCKER_REGISTRY}/${CSI_CONTROLLER_IMAGE}"
ctl_tag_specific="${ctl_registry}:${specific_tag}"
ctl_tag_latest=${ctl_registry}:latest
[ "$tag_latest" = "true" ] && taglatestflag="-t ${ctl_tag_latest}" 

echo "Build and push the CSI controller image"
docker build -t ${ctl_tag_specific} $taglatestflag -f Dockerfile-csi-controller --build-arg VERSION="${IMAGE_VERSION}" --build-arg BUILD_NUMBER="${BUILD_NUMBER}" .
docker push ${ctl_tag_specific}
[ "$tag_latest" = "true" ] && docker push ${ctl_tag_latest} || :

# CSI node
# --------
node_registry="${DOCKER_REGISTRY}/${CSI_NODE_IMAGE}"
node_tag_specific="${node_registry}:${specific_tag}"
node_tag_latest=${node_registry}:latest
[ "$tag_latest" = "true" ] && taglatestflag="-t ${node_tag_latest}" 

echo "Build and push the CSI node image"
docker build -t ${node_tag_specific} $taglatestflag -f Dockerfile-csi-node --build-arg VERSION="${IMAGE_VERSION}" --build-arg BUILD_NUMBER="${BUILD_NUMBER}" .
docker push ${node_tag_specific}
[ "$tag_latest" = "true" ] && docker push ${node_tag_latest} || :


set +x
echo ""
echo "Image ready:"
echo "   ${ctl_tag_specific}"
echo "   ${node_tag_specific}"
[ "$tag_latest" = "true" ] && { echo "   ${ctl_tag_latest}"; echo "   ${node_tag_latest}"; } || :

# if param $1 given the script echo the specific tag
[ -n "$1" ] && printf "${ctl_tag_specific}\n${node_tag_specific}\n" > $1 || :

