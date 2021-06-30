#!/bin/bash -xel
set +o pipefail

if [ "$driver_images_tag" == "develop" ]; then
  driver_images_tag=latest
fi
operator_image_for_test=$operator_image_repository_for_test:$operator_image_tag_for_test

kind_node_name=`docker ps --format "{{.Names}}"`
docker exec -i $kind_node_name apt-get update
docker exec -i $kind_node_name apt -y install open-iscsi

cd $(dirname $cr_file)
yq eval ".spec.controller.repository |= env(controller_repository_for_test)" $(basename $cr_file) -i
yq eval ".spec.controller.tag |= env(driver_images_tag)" $(basename $cr_file) -i
yq eval ".spec.node.repository |= env(node_repository_for_test)" $(basename $cr_file) -i
yq eval ".spec.node.tag |= env(driver_images_tag)" $(basename $cr_file) -i
cd -

cd $(dirname $operator_file)
operator_image_in_branch=`yq eval '(.spec.template.spec.containers[0].image | select(. == "*ibmcom*"))' $(basename $operator_file)`
sed -i "s+$operator_image_in_branch+$operator_image_for_test+g" $(basename $operator_file)
cd -

cat $operator_file | grep image:
cat $cr_file | grep repository:
cat $cr_file | grep tag:

kubectl apply -f $operator_file
kubectl apply -f $cr_file
