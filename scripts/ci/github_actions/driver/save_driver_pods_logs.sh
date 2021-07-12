#!/bin/bash -x

get_all_pods_by_type (){
    pod_type=$1
    kubectl get pod -l csi | grep $pod_type | awk '{print$1}'
}

run_action_on_pod (){
    pod_type=$1
    action=$2
    extra_args=$3
    kubectl $action $(get_all_pods_by_type $pod_type) $extra_args > "/tmp/driver_$(get_all_pods_by_type $pod_type)_${action}.txt"

}

declare -a pod_types=(
    "node"
    "controller"
    "operator"
)

for pod_type in "${pod_types[@]}"
do
    run_action_on_pod $pod_type logs "-c ibm-block-csi-$pod_type"
    run_action_on_pod $pod_type "describe pod" ""
done
