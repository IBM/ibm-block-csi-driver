#!/bin/bash -x

get_all_pods_by_type (){
    pod_name=$1
    kubectl get pod -l csi | grep $pod_name | awk '{print$1}'
}

run_action_on_pod (){
    pod_name=$1
    action=$2
    extra_args=$3
    kubectl $action $(get_all_pods_by_type $pod_name) $extra_args > "/tmp/driver_$(get_all_pods_by_type $pod_name)_${action}.txt"

}

declare -a pod_types=(
    "node"
    "controller"
    "operator"
)

for pod_type in "${pod_types[@]}"
do
    run_action_on_pod $pod_type logs "-c ibm-block-csi-$pod_name"
    run_action_on_pod $pod_type "describe pod" ""
done
