#!/bin/bash -xe
set +o pipefail

is_pods_ready (){
  pods=$@
  are_all_pods_ready=false
  for pod in $pods; do
    running_containers_count=`echo $pod | awk -F / '{print$1}'`
    total_containers_count=`echo $pod | awk -F / '{print$2}'`
    if [ $running_containers_count != $total_containers_count ]; then
      are_all_pods_ready=true
      break
    fi
  done
  echo $are_all_pods_ready
}

is_kubernetes_cluster_ready (){
  pods=`kubectl get pods -A | awk '{print$3}' | grep -iv ready`
  all_containers_are_running=false
  are_all_pods_ready=$(is_pods_ready $pods)
  if [ $are_all_pods_ready == "false" ]; then
    all_containers_are_running=true
  fi
  
  echo $all_containers_are_running
}

while [[ `is_kubernetes_cluster_ready` == "false" ]]; do
        kubectl get pods -A
done
