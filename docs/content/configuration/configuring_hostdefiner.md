
{{site.data.keyword.attribute-definition-list}}

# Configuring the host definer

Some of the parameters within the HostDefiner custom resource are configurable. Use this information to help decide whether the parameters for your storage system need to be updated.

Starting from 1.13.0 the configuation parameters are saved in the config map ibm-csi-hostdefiner-config and remain persistent on CSI software upgrade.

The old way, of adding the parameters in the hostdefiner yaml is obsolete!{: important}

The config map should be created in the same namespace as the hostdefiner pod.

Because the configuration is saved in a config map - it can, and should, be defined even before the upgrade to 1.13.0

If not created before upgrade or installation, for the configuration changes to take effect, the host definer must be restarted (by deleting the hostdefiner pod).

Use standard configmap CLIs to update the configuration, examples below.
All CLIs accept an optional -n parameter to specify the namespace

Create configmap in order to update a configuration parameter (replace "key" and "value" with actual parameter setting):
```
  kubectl create configmap ibm-csi-hostdefiner-config --from-literal=key=value
```
The key may be any key from the below table, under Field. The possible values are also specified in the table.

More than one value can be specified, as needed. For example:
```
  kubectl create configmap ibm-csi-hostdefiner-config --from-literal=connectivityType=fc --from-literal=portSet=portset64
```

View an existing ConfigMap
```
  kubectl get configmap ibm-csi-hostdefiner-config
```

Delete a ConfigMap:
```
  kubectl delete configmap ibm-csi-hostdefiner-config
```

Update Configmap:

In order to remove an entry or update the configmap, one can either use:
```
  kubectl edit configmap ibm-csi-hostdefiner-config
```

Which opens the editor to edit the config map, and is a live edit of the existing configmap,
OR you can 'patch' the config map by saving it, changing it, and reapplying:
```
  kubectl get configmap ibm-csi-hostdefiner-config -o yaml > ibm-csi-hostdefiner-config.yaml
```

edit yaml and then reapply:
```
  kubectl apply -f ibm-csi-hostdefiner-config.yaml
```

If update to configmap is needed, consider deleting it and recreating, as this may be easier than updating.

## Setting node to be managed by hostdefiner
If not using dynamicNodeLabeling (which is the default behavior), then to set a node to be managed by hostdefiner, meaning that hosts will be created for this node, label the node with `hostdefiner.block.csi.ibm.com/manage-node=true`
```
  kubectl label node <node-name1> <node-name2> hostdefiner.block.csi.ibm.com/manage-node=true
```

Consider [configuring dynamic host definition labels](../using/using_hostdefinition_labels.md) for node-specific customizations.{: tip}

For more information about using the host definer, see [Using dynamic host definition](../using/using_hostdefinition.md).

The prefix length is bound by the limitation of the storage system. When defined, the length is a combination of both the prefix and node (server) hostname.{: restriction}

When left blank, the connectivity type will update along with any changes within the host ports, according to the set hierarchy (see `connectivityType` description below). So this value can be especially important for setups that do not support NVMe. If the value is set and there are host port changes, the connectivity needs to be manually updated. For more information, see [Changing node connectivity](../using/changing_node_connectivity.md).{: attention}

As of this document's publication date, NVMe/FC is not supported for this release.{: restriction}

|Field|Description|
|---------|--------|
|`prefix`|Adds a prefix to the hosts defined by the host definer.|
|`connectivityType`|Selects the connectivity type for the host ports.<br>Possible input values are:<br>- `nvmeofc` for use with NVMe over Fibre Channel connectivity<br>- `fc` for use with Fibre Channel over SCSI connectivity<br>- `iscsi` for use with iSCSI connectivity<br>By default, this field is blank and the host definer selects the first of available connectivity types on the node, according to the following hierarchy: NVMe, FC, iSCSI.|
|`allowDelete`|Defines whether the host definer is allowed to delete host definitions on the storage system.<br>Input values are `true` or `false`.<br>The default value is `true`.|
|`dynamicNodeLabeling`|Defines whether the nodes that run the CSI node pod are dynamically labeled or if the user must create the `hostdefiner.block.csi.ibm.com/manage-node=true` label on each relevant node. This label tells the host definer which nodes to manage their host definition on the storage side.<br>Input values are `true` or `false`.<br>The default value is `false`, where the user must manually create this label on every node to be managed by the host definer for dynamic host definition on the storage.|
|`portSet`|FlashSystem specific field - Specifies the portset for new port definitions (ports already defined on the FlashSystem are not modified).|

## Example of getting configuration from old hostdefiner (pre-1.13.0) and creating the proper configmap for it:
Get the config changes from existing hostdefiner pod:
```
kubectl get pod host-definer-hostdefiner-xxxxxxxxxx-xxxxx -o yaml
```
in metadata.annotations.kubectl.kubernetes.io/last-applied-configuration, you'll be able to search for all the values mentioned in the above table. If they appear there, and are not the default (as specified in the above table), you should create a configmap for them prior to upgrading to 1.13.0.

For example, if this part looks like this:
```
apiVersion: v1
kind: Pod
metadata:
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: |
      {"apiVersion":"csi.ibm.com/v1","kind":"HostDefiner","metadata":{"annotations":{},"labels":{"app.kubernetes.io/instance":"ibm-block-csi","app.kubernetes.io/manag
ed-by":"ibm-block-csi-operator","app.kubernetes.io/name":"host-definer","release":"v1.12.5"},"name":"host-definer","namespace":"default"},"spec":{"hostDefiner":{"affi
nity":{"nodeAffinity":{"requiredDuringSchedulingIgnoredDuringExecution":{"nodeSelectorTerms":[{"matchExpressions":[{"key":"kubernetes.io/arch","operator":"In","values
":["amd64","s390x","ppc64le"]}]}]}}},"allowDelete":true,"connectivityType":"fc","dynamicNodeLabeling":true,"imagePullPolicy":"IfNotPresent","portSet":"portset64","
prefix":"myhost","repository":"quay.io/ibmcsiblock/ibm-block-csi-host-definer","tag":"1.12.5"}}}
```
then allowDelete is true, as is the default, connectivityType is fc - which is not default, dynamicNodeLabeling is true - which is not default, portSet is portset64 and prefix is myhost. The configmap that should be created is like this:
```
kubectl create configmap ibm-csi-hostdefiner-config --from-literal=connectivityType=fc --from-literal=portSet=portset64 --from-literal=dynamicNodeLabeling="true" --from-literal=prefix="myhost"
```
and then viewing it:
```
kubectl get configmap ibm-csi-hostdefiner-config -o yaml

apiVersion: v1
data:
  connectivityType: fc
  dynamicNodeLabeling: "true"
  portSet: portset64
  prefix: myhost
kind: ConfigMap
metadata:
  creationTimestamp: "2026-02-02T05:40:05Z"
  name: ibm-csi-hostdefiner-config
  namespace: default
  resourceVersion: "1010025"
  uid: 455403ea-dc0c-457f-ab88-9b0e4aa0b0fb

```
