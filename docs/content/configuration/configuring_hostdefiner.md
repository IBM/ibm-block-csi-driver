# Configuring the host definer

Some of the parameters within the HostDefiner custom resource are configurable. Use this information to help decide whether the parameters for your storage system need to be updated.

For more information about using the host definer, see [Using dynamic host definition](../using/using_hostdefinition.md).
    
|Field|Description|
|---------|--------|
|`prefix`|Adds a prefix to the hosts defined by the host definer.<br>**Note:** The prefix length is bound by the limitation of the storage system. When defined, the length is a combination of both the prefix and node (server) hostname.|
|`connectivityType`|Selects the connectivity type for the host ports.<br>Possible input values are:<br>- `nvmeofc` for use with NVMe over Fibre Channel connectivity<br>- `fc` for use with Fibre Channel over SCSI connectivity<br>- `iscsi` for use with iSCSI connectivity<br>By default, this field is blank and the host definer selects the first of available connectivity types on the node, according to the following hierarchy: NVMe, FC, iSCSI.|
|`allowDelete`|Defines whether the host definer is allowed to delete host definitions on the storage system.<br>Input values are `true` or `false`.<br>The default value is `true`.|
|`dynamicNodeLabeling`|Defines whether the nodes that run the CSI node pod are dynamically labeled or if the user must create the `hostdefiner.block.csi.ibm.com/manage-node=true` label on each relevant node. This label tells the host definer which nodes to manage their host definition on the storage side.<br>Input values are `true` or `false`.<br>The default value is `false`, where the user must manually create this label on every node to be managed by the host definer for dynamic host definition on the storage.|

The following is an example of a HostDefiner yaml file, where the following have been manuallly changed: `prefix` is set as _demo-prefix_ and the `connectivityType` is set to _fc_.

```
apiVersion: csi.ibm.com/v1
kind: HostDefiner
metadata:
  name: host-definer
  namespace: default
  labels:
    app.kubernetes.io/name: host-definer
    app.kubernetes.io/instance: ibm-block-csi
    app.kubernetes.io/managed-by: ibm-block-csi-operator
    release: v1.10.0
spec:
  hostDefiner:
#    prefix: demo-prefix            # Optional.
#    connectivityType: fc           # Optional. Values nvme/fc/iscsi. The default is chosen dynamically.
#    allowDelete: true             # Optional. Values true/false. The default is true.
#    dynamicNodeLabeling: false    # Optional. Values true/false. The default is false.
    repository: ibmcom/ibm-block-csi-host-definer
    tag: "1.10.0"
    imagePullPolicy: IfNotPresent
    affinity:
      nodeAffinity:
        requiredDuringSchedulingIgnoredDuringExecution:
          nodeSelectorTerms:
            - matchExpressions:
                - key: kubernetes.io/arch
                  operator: In
                  values:
                    - amd64
                    - s390x
                    - ppc64le
#    tolerations:
#    - effect: NoSchedule
#      key: node-role.kubernetes.io/master
#      operator: Exists
#  imagePullSecrets:
#  - "secretName"
```