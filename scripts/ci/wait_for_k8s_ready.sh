#!/bin/bash -xe
set +o pipefail

is_kubernetes_cluster_ready (){
  pods=`kubectl get pods -A | awk '{print$3}' | grep -iv ready`
  all_the_containers_are_runninig=true
  for pod in $pods; do
    running_containers_count=`echo $pod | awk -F / '{print$1}'`
    total_containers_count=`echo $pod | awk -F / '{print$2}'`
    if [ $running_containers_count != $total_containers_count ]; then
      all_the_containers_are_runninig=false
      break
    fi
  done
  echo $all_the_containers_are_runninig
}

while [[ `is_kubernetes_cluster_ready` == "false" ]]; do
        kubectl get pods -A
done

