# Known issues

This section details the known issues in IBM® block storage CSI driver 1.10.0, along with possible solutions or workarounds (if available).

The following severity levels apply to known issues:

-   **HIPER** – High Impact Pervasive. A critical issue that IBM has either fixed or plans to fix promptly. Requires immediate customer attention or code upgrade.
-   **High Impact** – Potentially irrecoverable error that might impact data or access to data in rare cases or specific situations/configurations.
-   **Moderate** – Limited functionality issue and/or performance issue with a noticeable effect.
-   **Service** – Non-disruptive recoverable error that can be resolved through a workaround.
-   **Low** – Low-impact usability-related issue.

**Important:**

-   **The issues listed below apply to IBM block storage CSI driver 1.10.0**. As long as a newer version has not yet been released, a newer release notes edition for IBM block storage CSI driver 1.10.0 might be issued to provide a more updated list of known issues and workarounds.
-   When a newer version is released for general availability, the release notes of this version will no longer be updated. Accordingly, check the release notes of the newer version to learn whether any newly discovered issues affect IBM block storage CSI driver 1.10.0 or whether the newer version resolves any of the issues listed below.

|Ticket ID|Severity|Description|
|---------|--------|-----------|
|**CSI-4555**|Service|In rare cases when using dynamic host definition and `connectivityType` is defined as `fc`, only one WWPN port is defined on the storage. When more than one WWPN port on the worker node is defined this can cause I/O issues or a single point of failure.<br>**Workaround:** Ensure that all host ports are properly configured on the storage system. If the issue continues and the CSI driver can still not attach a pod, contact IBM Support.|
|**CSI-4446**|Service|In extremely rare cases, the HostDefiner `hostdefiner.block.csi.ibm.com/manage-node=true` labels are not deleted during csinode deletion from the nodes. This occurs even when the `allowDelete` and `dynamicNodeLabeling` parameters are set to `true`.<br>**Workaround:** Manually delete the hosts from the storage system.|
|**CSI-3382**|Service|After CSI Topology label deletion, volume provisioning does not work, even when not using any topology-aware YAML files.<br>**Workaround:** To allow volume provisioning through the CSI driver, delete the operator pod. <br>After the deletion, a new operator pod is created and the controller pod is automatically restarted, allowing for volume provisioning.|
|**CSI-2157**|Service|In extremely rare cases, too many Fibre Channel worker node connections may result in a failure when the CSI driver attempts to attach a pod. As a result, the `Host for node: {0} was not found, ensure all host ports are configured on storage` error message may be found in the IBM block storage CSI driver controller logs. <br>**Workaround:** Ensure that all host ports are properly configured on the storage system. If the issue continues and the CSI driver can still not attach a pod, contact IBM Support.|
|**CSI-4554**|Low|During HostDefinition creation no success event is emitted when the status is in a _Ready_ state.<br>**Workaround:** No workaround available.|

