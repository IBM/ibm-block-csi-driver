# Creating a StorageClass

Create a storage class yaml file in order to define the storage system pool name, secret reference, SpaceEfficiency, and fstype.

1.  Use the following procedure to create and apply the storage classes.

    **Note:** This procedure is applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace kubectl with oc in all relevant commands.

2.  Create a storage class yaml file, similar to the following demo-storageclass.yaml.

    Update the capabilities, pools, and array secrets, as needed.

    Use the SpaceEfficiency parameters for each storage system, as defined in [Table 1](#spaceefficiency). These values are not case-sensitive.

    |Storage system type|SpaceEfficiency parameter options|
    |-------------------|---------------------------------|
    |IBM FlashSystem® A9000 and A9000R|Always includes deduplication and compression.No need to specify during configuration.

|
    |IBM Spectrum® Virtualize Family|    -   thick \(default value\)
    -   thin
    -   compressed
    -   deduplicated
 **Note:** If not specified, the default value is thick.

|
    |IBM® DS8000® Family|    -   none \(default value\)
    -   thin
 **Note:** If not specified, the default value is color:blue;none.

|

    -   The IBM DS8000 Family pool value is the pool ID and not the pool name as is used in other storage systems.
    -   The pool value should be a name of an existing pool on the storage system.
    -   The allowVolumeExpansion parameter is optional but is necessary for using volume expansion. The default value is false.

        **Note:** Be sure to set the value to true to allow volume expansion.

    -   The csi.storage.k8s.io/fstype parameter is optional. The values that are allowed are ext4 or xfs. The default value is ext4.
    -   The volume\_name\_prefix parameter is optional.

        **Note:** For IBM DS8000 Family, the maximum prefix length is five characters.The maximum prefix length for other systems is 20 characters.

        color:blue;For storage systems using Spectrum Virtualize, the CSI\_ prefix is added as default if not specified by the user.

    ```screen
    kind: StorageClass
    apiVersion: storage.k8s.io/v1
    metadata:
      name: demo-storageclass
    provisioner: block.csi.ibm.com
    parameters:
      SpaceEfficiency: deduplicated   \# Optional.
      pool: demo-pool
    
      csi.storage.k8s.io/provisioner-secret-name: demo-secret
      csi.storage.k8s.io/provisioner-secret-namespace: default
      csi.storage.k8s.io/controller-publish-secret-name: demo-secret
      csi.storage.k8s.io/controller-publish-secret-namespace: default
      csi.storage.k8s.io/controller-expand-secret-name: demo-secret
      csi.storage.k8s.io/controller-expand-secret-namespace: default
    
      csi.storage.k8s.io/fstype: xfs   \# Optional. Values ext4\\xfs. The default is ext4.
      volume\_name\_prefix: demoPVC      \# Optional.
    allowVolumeExpansion: true
    ```

3.  Apply the storage class.

    ```
    kubectl apply -f demo-storageclass.yaml
    ```

    The `storageclass.storage.k8s.io/demo-storageclass created` message is emitted.


