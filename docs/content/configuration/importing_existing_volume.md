# Importing an existing volume

Use this information to import volumes that were created externally from the IBM® block storage CSI driver by using a persistent volume (PV) YAML file.

Before starting to import an existing volume, find the `volumeHandle` in the existing volume in order to include the information in the persistent volume (PV) YAML file. To find the `volumeHandle`, use one of the following procedures:

- **For IBM Storage Virtualize family**

  The `volumeHandle` is formatted as `SVC:id;vdisk_UID`.

  - Through command line:
    Find both the `id` and `vdisk_UID` attributes, by using the `lsvdisk` command.

    For more information, see **Command-line interface** > **Volume commands** > **lsvdisk** within your specific product documentation on [IBM Documentation](https://www.ibm.com/docs/).

  - Through the management GUI:

    1. Select **Volumes** > **Volumes** from the side bar.

        The **Volumes** page is displayed.

    2. Browse to the volume that the port is on and right-click > **Properties**.

      The Properties window is displayed. Use the **Volume ID** and **Volume UID** values.

    For more information about IBM Storage Virtualize products, find your product information in [IBM Documentation](https://www.ibm.com/docs/).
  


- **For IBM DS8000 family:**

  The `volumeHandle` is formatted as `DS8K:id;GUID`.
  The `id` is the last four digits of the `GUID`.

  - Through the command line:

    Find the `GUID` for the volume, by using the `lsfbvol` command.

     For more information, see **Reference** > **Command-line interface** > **CLI commands** > **Storage configuration commands** > **Fixed block logical volume specific commands** > **lsfbvol** within your specific product documentation on [IBM Documentation](https://www.ibm.com/docs/).

  - Through the IBM DS8000 Storage Management GUI:

    1. Select **Volumes** from the side bar.

        The **Volumes** page is displayed.

    2. Browse to the volume that the port is on and right-click > **Properties**.

        The Properties window is displayed. Use the **GUID** value.

    For more information about IBM DS8000 family products, find your product information in [IBM Documentation](https://www.ibm.com/docs/).
  

Use this procedure to help build a PV YAML file for your volumes.

**Note:** These steps are set up for importing volumes from an IBM Storage Virtualize family system. Change parameters, as needed.

1. Create a persistent volume (PV) YAML file.

    **Important:** Be sure to include the `storageClassName` and `controllerPublishSecretRef` parameters or errors may occur.

2. Take the `volume_name` and other optional information (collected before the procedure) and insert it into the YAML file (under `spec.csi.volumeAttributes`).

    **Important:** If using the CSI Topology feature, the `spec.csi.volumeHandle` contains the management ID (see [Creating a StorageClass with topology awareness](creating_storageclass_topology_aware.md)). In the example below, the `spec.csi.volumeHandle` would read similar to the following:<br> `SVC:demo-system-id-1:0;600507640082000B08000000000004FF`
    
    ```
    apiVersion: v1
    kind: PersistentVolume
    metadata:
      annotations: 
        pv.kubernetes.io/provisioned-by: block.csi.ibm.com
      name: demo-pv
    spec:
      accessModes:
      - ReadWriteOnce
      capacity:
        storage: 1Gi
      csi:
        fsType: ext4
        controllerExpandSecretRef:
          name: demo-secret
          namespace: default
        controllerPublishSecretRef:
          name: demo-secret
          namespace: default
        nodePublishSecretRef:
          name: demo-secret
          namespace: default
        nodeStageSecretRef:
          name: demo-secret
          namespace: default
        driver: block.csi.ibm.com
        volumeHandle: SVC:id;uid
      storageClassName: demo-storageclass
      persistentVolumeReclaimPolicy: Retain
    ```

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
