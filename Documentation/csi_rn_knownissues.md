# Known issues

This section details the known issues in IBM® block storage CSI driver 1.6.0, along with possible solutions or workarounds \(if available\).

The following severity levels apply to known issues:

-   **HIPER** – High Impact Pervasive. A critical issue that IBM has either fixed or plans to fix promptly. Requires immediate customer attention or code upgrade.
-   **High Impact** – Potentially irrecoverable error that might impact data or access to data in rare cases or specific situations/configurations.
-   **Moderate** – Limited functionality issue and/or performance issue with a noticeable effect.
-   **Service** – Non-disruptive recoverable error that can be resolved through a workaround.
-   **Low** – Low-impact usability-related issue.

**Important:**

-   **The issues listed below apply to IBM block storage CSI driver 1.6.0**. As long as a newer version has not yet been released, a newer release notes edition for IBM block storage CSI driver 1.6.0 might be issued to provide a more updated list of known issues and workarounds.
-   When a newer version is released for general availability, the release notes of this version will no longer be updated. Accordingly, check the release notes of the newer version to learn whether any newly discovered issues affect IBM block storage CSI driver 1.6.0 or whether the newer version resolves any of the issues listed below.

|Ticket ID|Severity|Description|
|---------|--------|-----------|
|**CSI-2157**|Service|In extremely rare cases, too many Fibre Channel worker node connections may result in a failure when the CSI driver attempts to attach a pod. As a result, the `Host for node: \{0\} was not found, ensure all host ports are configured on storage` error message may be found in the IBM block storage CSI driver controller logs.<br />**Workaround:** Ensure that all host ports are properly configured on the storage system. If the issue continues and the CSI driver can still not attach a pod, contact IBM Support.|
|**CSI-702**|Service|Modifying the controller or node **affinity** settings may not take effect.<br />**Workaround:** If needed, delete the controller StatefulSet and/or the DaemonSet node after modifying the **affinity** settings in the IBMBlockCSI custom resource.|

