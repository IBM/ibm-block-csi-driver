# Installing the `HostDefinition` custom resource

Install the `HostDefinition` custom resource to enable automatic host definitions from within the CSI driver.

Use the following steps to install the `HostDefinition` custom resource, with [GitHub](https://github.com/IBM/ibm-block-csi-operator).

**Note:** Only hosts added after installation are automatically defined with the CSI driver.

1. Download the manifest from GitHub.

        ```
        curl https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/v1.10.0/deploy/installer/generated/host-definition.yaml > host-definition.yaml
        ```

2. Verify that the hostdefinition is in the _Ready_ phase.

    ```
    $> kubectl get hostdefinition
    NAME                  AGE    PHASE   STORAGE          HOST
    <k8s_resource_name1>  102m   Ready   <storage_name>   <host_name_w1>
    k8s_resource_name2>  102m   Ready   <storage_name>   <host_name_w2>
    ```

    If in an `Error` state, a retry can be forced using TBD.
    
3.  (Optional) Update the prefix and connectivity fields in the `HostDefiner.yaml`.

    |Field|Description|
    |---------|--------|
    |prefix|Adds a prefix to the hosts defined by the CSI driver.|
    |connectivity|Selects the connectivity type for the host ports.<br>Possible input values are:<br>- `iscsi` for use with iSCSI connectivity<br>- `fc` for use with Fibre Channel over SCSI connectivity<br>- `nvme` for use with NVME over Fibre Channelconnectivity (Spectrum Virtualize storage systems only)<br>By default, this field is blank and the driver selects the strongest of available connectivity options.|
