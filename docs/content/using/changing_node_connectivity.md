
{{site.data.keyword.attribute-definition-list}}

# Changing node connectivity

Node connectivity for dynamic host definition is done dynamically. In some situations connectivity may need to be updated manually.

The following are examples of when dynamic host definition can occur:
- If ports connectivity is changed on the host and restart to the relevant CSI node port.
- If connectivity has changed on either the custom resource or label.

For more information, see [Configuring the host definer](../configuration/configuring_hostdefiner.md).

## Manually changing node connectivity

In some situations, node connectivity may need to be manually configured.

Before you begin, if the `allowDelete` parameter is set to `false`, ensure that the old host definition is deleted.{: important}

Use the following procedure to redefine host connectivity:

1. Undeploy the CSI node pod from the relevant node that the host definition is a part of.
This can be done by editing the affinity, and not writing the relevant node:
```
  kubectl edit ibmblockcsis ibm-block-csi
```
under section spec.node.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms.matchExpressions add this:
```
               - key: kubernetes.io/hostname
                 operator: In
                 values:
                 - <node-names-we-want-to-keep-as-appear-in-kubectl-get-node>
                 - <another-node-name-we-want-to-keep>
```

2. Verify that all HostDefinition instances of the node are deleted. The output of the following command displays all HostDefinitions that do not need to be deleted for the `<node-name>`.
```
kubectl get hostdefinitions -o=jsonpath='{range .items[?(@.spec.hostDefinition.nodeName=="<node-name>")]}{.metadata.name}{"\n"}{end}'
```

3. From the node, perform the connectivity changes.

4. Redeploy the CSI node pod on the relevant node.

   The host definer handles the creation of the new host definition on the storage side.

