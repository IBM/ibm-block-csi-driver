# Creating a StorageClass

Create a storage class yaml file in order to define the storage system pool name, secret reference, `SpaceEfficiency`, and `fstype`.

Use the following procedure to create and apply the storage classes.

**Note:** This procedure is applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.

1. Create a storage class yaml file, similar to the following demo-storageclass.yaml.

    Update the capabilities, pools, and array secrets, as needed.

    Use the `SpaceEfficiency` parameters for each storage system, as defined in [the following table](#spaceefficiency). These values are not case-sensitive.

    <table><thead><b><a name="spaceefficiency">Table:</a></b>  <i> <code>SpaceEfficiency</code> parameter definitions per storage system type</i></thead>
      <tbody>
        <tr>
          <th><b>Storage system type</b></th>
          <th><b>SpaceEfficiency parameter options</b></th>
         </tr>
         <tr>
          <td>IBM FlashSystem® A9000 and A9000R</td>
          <td>Always includes deduplication and compression. No need to specify during configuration.</td>
        </tr>
        <tr>
          <td>IBM Spectrum® Virtualize Family</td>
          <td>
            <ul>
            <li><code>thick</code> (default value)</li>
            <li><code>thin</code></li><li><code>compressed</code></li>
            <li><code>deduplicated</code></li>
            <b>Note:</b> If not specified, the default value is <code>thick</code>.</li>
            </ul></td>
        </tr>
        <tr>
          <td>IBM® DS8000® Family</td>
          <td>
            <ul>
            <li><code>none</code> (default value)</li>
            <li><code>thin</code></li>
            <b>Note:</b> If not specified, the default value is none.</li>
            </ul>
            </td>
        </tr>
      </tbody>
     </table> 


    - The IBM DS8000 Family `pool` value is the pool ID and not the pool name as is used in other storage systems.
    - The `pool` value should be a name of an existing pool on the storage system.
    - The `allowVolumeExpansion` parameter is optional but is necessary for using volume expansion. The default value is _false_.

    **Note:** Be sure to set the value to true to allow volume expansion.

    - The `csi.storage.k8s.io/fstype` parameter is optional. The values that are allowed are _ext4_ or _xfs_. The default value is _ext4_.
    - The `volume_name_prefix` parameter is optional.

    **Note:** For IBM DS8000 Family, the maximum prefix length is five characters.The maximum prefix length for other systems is 20 characters.<br />For storage systems using Spectrum Virtualize, the `CSI_` prefix is added as default if not specified by the user.

        kind: StorageClass
        apiVersion: storage.k8s.io/v1
        metadata:
          name: demo-storageclass
        provisioner: block.csi.ibm.com
        parameters:
          SpaceEfficiency: deduplicated   # Optional.
          pool: demo-pool
        
          csi.storage.k8s.io/provisioner-secret-name: demo-secret
          csi.storage.k8s.io/provisioner-secret-namespace: default
          csi.storage.k8s.io/controller-publish-secret-name: demo-secret
          csi.storage.k8s.io/controller-publish-secret-namespace: default
          csi.storage.k8s.io/controller-expand-secret-name: demo-secret
          csi.storage.k8s.io/controller-expand-secret-namespace: default
        
          csi.storage.k8s.io/fstype: xfs   # Optional. Values ext4\xfs. The default is ext4.
          volume_name_prefix: demoPVC      # Optional.
        allowVolumeExpansion: true

2.  Apply the storage class.

    ```
    kubectl apply -f demo-storageclass.yaml
    ```

    The `storageclass.storage.k8s.io/demo-storageclass created` message is emitted.


