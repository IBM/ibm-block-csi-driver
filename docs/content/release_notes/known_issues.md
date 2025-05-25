
{{site.data.keyword.attribute-definition-list}}

# Known issues

This section details the known issues in IBM® block storage CSI driver 1.12.3, along with possible solutions or workarounds (if available).

The following severity levels apply to known issues:

-   **HIPER** – High Impact Pervasive. A critical issue that IBM has either fixed or plans to fix promptly. Requires immediate customer attention or code upgrade.
-   **High Impact** – Potentially irrecoverable error that might impact data or access to data in rare cases or specific situations/configurations.
-   **Moderate** – Limited functionality issue and/or performance issue with a noticeable effect.
-   **Service** – Non-disruptive recoverable error that can be resolved through a workaround.
-   **Low** – Low-impact usability-related issue.


**The issues listed below apply to IBM® block storage CSI driver 1.12.3**. As long as a newer version has not yet been released, a newer release notes edition for IBM® block storage CSI driver 1.12.3 might be issued to provide a more updated list of known issues and workarounds.{: important}

When a newer version is released for general availability, the release notes of this version will no longer be updated. Accordingly, check the release notes of the newer version to learn whether any newly discovered issues affect IBM block storage CSI driver 1.12.3 or whether the newer version resolves any of the issues listed below.

|Ticket ID|Severity|Description|
|---------|--------|-----------|
|**CSI-5231**|Service|In some cases, when the volume group selector information is updated, triggering an add or remove PVC operation the following can occur:<br/> - No PVC update events are emitted.<br/>- Finalizers are not added or removed to all of the PVCs. In these cases the PVCs can potentially be deleted even when part of a volume group. <br>**Workaround:** To prevent this issue from occurring, trigger add or remove PVC operations by editing the PVC volume group label (by either adding or removing the label).<br/>If the volume group selector has been updated, manually update the finalizer with in the PVC.<br/> - To add a finalizer to an added PVC, use the following command:<br>`kubectl patch pvc <pvc_name> -p '{"metadata":{"finalizers":["volumegroup.storage.ibm.io/pvc-protection"]}}'`<br>- To remove a specific finalizer, use the `kubectl edit pvc <pvc_name>` and then remove the `volumegroup.storage.ibm.io/pvc-protection` finalizer from the finalizer list.<br>- To remove all finalizers from a PVC, use the following command: `kubectl patch pvc <pvc_name> -p '{"metadata":{"finalizers":null}}'`|
|**CSI-4555**|Service|In rare cases when using dynamic host definition and `connectivityType` is defined as `fc`, only one WWPN port is defined on the storage. When more than one WWPN port on the worker node is defined this can cause I/O issues or a single point of failure.<br>**Workaround:** Ensure that all host ports are properly configured on the storage system. If the issue continues and the CSI driver can still not attach a pod, contact IBM Support.|
|**CSI-4446**|Service|In extremely rare cases, the HostDefiner `hostdefiner.block.csi.ibm.com/manage-node=true` labels are not deleted during csinode deletion from the nodes. This occurs even when the `allowDelete` and `dynamicNodeLabeling` parameters are set to `true`.<br>**Workaround:** Manually delete the hosts from the storage system.|
|**CSI-3382**|Service|After CSI Topology label deletion, volume provisioning does not work, even when not using any topology-aware YAML files.<br>**Workaround:** To allow volume provisioning through the CSI driver, delete the operator pod. <br>After the deletion, a new operator pod is created and the controller pod is automatically restarted, allowing for volume provisioning.|
|**CSI-2157**|Service|In extremely rare cases, too many Fibre Channel worker node connections may result in a failure when the CSI driver attempts to attach a pod. As a result, the `Host for node: {0} was not found, ensure all host ports are configured on storage` error message may be found in the IBM block storage CSI driver controller logs. <br>**Workaround:** Ensure that all host ports are properly configured on the storage system. If the issue continues and the CSI driver can still not attach a pod, contact IBM Support.|
|**CSI-5722**|Service|In rare cases, when recreating a pod with a previously used PVC, volume attachment may be stuck and needs to be manually released <br>**Workaround:** get the list of volume attachments and find the one that is stuck, then release the volume attachment by deleting any finalizers. Then recreate the pod with the previously used PVC.|
|**CSI-5841**|Service|NVMe/FC support not working properly on RHEL 8 & 9 due to use of incompatible SCSI and multipath tools by CSI. As of this document's publication date, NVMe/FC is not supported for this release.|

