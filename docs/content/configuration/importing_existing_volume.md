# Importing an existing volume

Use this information to import volumes that were created externally from the IBM® block storage CSI driver by using a persistent volume (PV) YAML file.

Before starting to import an existing volume, find the `volumeHandle` in the existing volume in order to include the information in the persistent volume (PV) YAML file. To find the `volumeHandle`, use one of the following procedures:

- **For Spectrum Virtualize family**

  The `volumeHandle` is formatted as `SVC:id;vdisk_UID`.

  - Through command line:
    Find both the `id` and `vdisk_UID` attributes, by using the `lsvdisk` command.

    For more information, see **Command-line interface** > **Volume commands** > **lsvdisk** within your specific product documentation on [IBM Documentation](https://www.ibm.com/docs/).

  - Through the management GUI:

    1. Select **Volumes** > **Volumes** from the side bar.

        The **Volumes** page is displayed.

    2. Browse to the volume that the port is on and right-click > **Properties**.

      The Properties window is displayed. Use the **Volume ID** and **Volume UID** values.

    For more information about Spectrum Virtualize products, find your product information in [IBM Documentation](https://www.ibm.com/docs/).
  
- **For FlashSystem A9000 and A9000R:**

  The `volumeHandle` is formatted as `A9000:id;WWN`.
  
  - Through command line:

    Find the `id` and `WWN` for the volume, by using the `vol_list -f` command.

    For more information, see **Reference** > **Command-line reference (12.3.2.x)** > **Volume management commands** > **Listing volumes** within your specific product documentation on [IBM Documentation](https://www.ibm.com/docs/).

  - Through the Hyper-Scale Management user interface:

    1. Select **Pools and Volumes Views** > **Volumes** from the side bar.

        The **Volumes** table is displayed.

    2. Select the `Volume`.

        The **Volume Properties** form is displayed.

    3. Use the **ID** and **WWN** values.
    
    For more information, see [IBM Hyper-Scale Manager documentation](https://www.ibm.com/docs/en/hyper-scale-manager/).

- **For DS8000 family:**

  The `volumeHandle` is formatted as `DS8K:id;GUID`.
  The `id` is the last four digits of the `GUID`.

  - Through the command line:

    Find the `GUID` for the volume, by using the `lsfbvol` command.

     For more information, see **Reference** > **Command-line interface** > **CLI commands** > **Storage configuration commands** > **Fixed block logical volume specific commands** > **lsfbvol** within your specific product documentation on [IBM Documentation](https://www.ibm.com/docs/).

  - Through the DS8000 Storage Management GUI:

    1. Select **Volumes** from the side bar.

        The **Volumes** page is displayed.

    2. Browse to the volume that the port is on and right-click > **Properties**.

        The Properties window is displayed. Use the **GUID** value.

    For more information about DS8000 family products, find your product information in [IBM Documentation](https://www.ibm.com/docs/).
  

Use this procedure to help build a PV YAML file for your volumes.

**Note:** These steps are set up for importing volumes from a Spectrum Virtualize family system. Change parameters, as needed.

1. Create a persistent volume (PV) YAML file.

    **Important:** Be sure to include the `storageClassName` and `controllerPublishSecretRef` parameters or errors may occur.

2. Take the `volume_name` and other optional information (collected before the procedure) and insert it into the YAML file (under `spec.csi.volumeAttributes`).

    **Important:** If using the CSI Topology feature, the `spec.csi.volumeHandle` contains the management ID (see [Creating a StorageClass with topology awareness](creating_storageclass_topology_aware.md)). In the example below, the `spec.csi.volumeHandle` would read similar to the following: `SVC:demo-system-id-1:0;600507640082000B08000000000004FF`.
    
          apiVersion: v1
          kind: PersistentVolume
          metadata:
            # annotations:
              # pv.kubernetes.io/provisioned-by: block.csi.ibm.com
            name: demo-pv
          spec:
            accessModes:
            - ReadWriteOnce
            capacity:
              storage: 1Gi
            csi:
              controllerExpandSecretRef:
                name: demo-secret-2
                namespace: default
              controllerPublishSecretRef:
                name: demo-secret-2
                namespace: default
              nodePublishSecretRef:
                name: demo-secret-2
                namespace: default
              nodeStageSecretRef:
                name: demo-secret-2
                namespace: default
            # fsType: ext4
              driver: block.csi.ibm.com
              # volumeAttributes:
                # pool_name: demo-pool
                # storage_type: SVC
                # volume_name: demo-prefix_demo-pvc-file-system
                # array_address: demo-management-address
              volumeHandle: SVC:0;600507640082000B08000000000004FF
            # persistentVolumeReclaimPolicy: Retain
            storageClassName: demo-storageclass
            # volumeMode: Filesystem

3. Create a PersistentVolumeClaim (PVC) YAML file.

    **Note:**

    - Be sure to include the `storageClassName`.
    - For more information about creating a PVC YAML file, see [Creating a PersistentVolumeClaim (PVC)](creating_pvc.md).
    
    ```
    apiVersion: v1
    kind: PersistentVolumeClaim
    metadata:
      name: demo-pvc
    spec:
      accessModes:
      - ReadWriteOnce
      resources:
        requests:
          storage: 1Gi
      storageClassName: demo-storageclass
      volumeName: demo-pv
    ```