#!/bin/sh
#
# Copyright 2024- IBM Inc. All rights reserved
# SPDX-License-Identifier: MIT
#

#set -x

# --- ADJUST THESE VARIABLES ACCORDING TO YOUR NEEDS
PVC="rwo-to-rwx"
SC="v7000-ctr-66-fs840"
# The namespace needs to exist
NAMESPACE="rwx-poc"
# ---

wait_for_pvc_bound() {
    PHASE=$(oc get pvc $1 -o 'jsonpath={.status.phase}')
    while [ "$PHASE" != "Bound" ]; do
	sleep 1
	PHASE=$(oc get pvc $1 -o 'jsonpath={.status.phase}')
    done
}

# Change to namespace
oc project $NAMESPACE

# Create PVC
cat <<EOF|oc create -f -
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: $PVC
  namespace: $NAMESPACE
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
  storageClassName: $SC
  volumeMode: Block
EOF

wait_for_pvc_bound $PVC

# Retrieve PV for PVC
PV=$(oc get pvc $PVC -o jsonpath='{.spec.volumeName}')

# Ensure that the PV will be retained after PVC deletion
oc patch pv $PV --type=merge -p '{"spec": {"persistentVolumeReclaimPolicy": "Retain"}}'

# Delete PVC
oc delete pvc $PVC

# Check that PV status is "Released"
oc get pv $PV -o jsonpath='{.status.phase}{"\n"}'

# Cleanup PV uid reference that prevents PV reclamation
oc patch pv $PV --type='json' -p='[{"op": "remove", "path": "/spec/claimRef/uid"}]'

# Check that status is "Available"
oc get pv $PV -o jsonpath='{.status.phase}{"\n"}'

# Change PV access mode to RWX
oc patch pv $PV --type='json' -p='[{"op": "replace", "path": "/spec/accessModes/0", "value": "ReadWriteMany"}]'

# Re-create PVC
cat <<EOF|oc create -f -
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: $PVC
  namespace: $NAMESPACE
spec:
  accessModes:
    - ReadWriteMany
  resources:
    requests:
      storage: 1Gi
  storageClassName: $SC
  volumeMode: Block
EOF

wait_for_pvc_bound $PVC

# Check that it is now RWX and bound to the same PV
echo "Original PV: $PV"
oc get pvc $PVC
