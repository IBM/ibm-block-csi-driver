# Importing an existing volume

Use this information to import volumes created externally from the IBM® block storage CSI driver by using a persistent volume (PV) YAML file.

Before starting to import an existing volume, find the following information in the existing volume in order to include the information in the persistent volume (PV) YAML file:
- `volumeHandle`
- `volumeAttributes` (optional)
  
  Including:

    - `pool_name`: _Name of Pool where volume is located_ (Listed as `pool_id` for DS8000® Family systems.)
    - `storage_type`: <`SVC` | `A9000` | `DS8K`>
    - `volume_name`: _Volume name_
    - `array_address`: _Array address_

To find the `volumeHandle`, use one of the following procedures:


- **Through command line (for Spectrum Virtualize Family):**

  - Find the `vdisk_UID` attribute, by using the `lsvdisk` command.
  - In order to enable the remote copy function find the `ID` attribute. This is also found by using the `lsvdisk` command.
  
  For more information, see **Command-line interface** > **Volume commands** > **lsvdisk** within your specific product documentation on [IBM Docs](https://www.ibm.com/docs/en).

- **Through command line (for FlashSystem A9000 and A9000R):**

  Find the WWN for the volume, by using the `vol_list_extended` command.
  
  For more information, see **Reference** > **Command-line reference (12.3.2.x)** > **Volume management commands** > **Listing a volume's extended attributes** within your specific product documentation on [IBM Docs](https://www.ibm.com/docs/en).

- **Through the Spectrum Virtualize management GUI:**

  1. Select **Volumes** > **Volumes** from the side bar.

     The **Volumes** page appears.

  2. Browse to the volume that the port is on and right-click > **Properties**.

     The Properties window appears. Use the UID number.

     For more information about Spectrum Virtualize products, find your product information in [IBM Documentation](https://www.ibm.com/docs/).

- **Through the IBM Hyper-Scale Manager user interface for FlashSystem A9000 and A9000R storage systems:**

  1. Select **Pools and Volumes Views** > **Volumes** from the side bar.

      The **Volumes** table is displayed.

  2. Select the `Volume`.

      The **Volume Properties** form appears.

  3. Use the **ID** number.
    
      For more information, see [IBM Hyper-Scale Manager documentation](https://www.ibm.com/docs/en/hyper-scale-manager/).


Use this procedure to help build a PV YAML file for your volumes.

**Note:** These steps are setup for importing volumes from a Spectrum Virtualize Family system. Change parameters, as needed.

1. Create a persistent volume (PV) YAML file.

    **Important:** Be sure to include the `storageClassName` and `controllerPublishSecretRef` parameters or errors will occur.

2. Take the `volume_name` and other optional information (collected before the procedure) and insert it into the YAML file (under `spec.csi.volumeAttributes`).

    **Important:** If using the CSI Topology feature, the `spec.csi.volumeHandle` contains the system ID. In the example below, the `spec.csi.volumeHandle` would read similar to the following: `SVC:demo-system-id-1:600507640082000B08000000000004FF`.
    
        apiVersion: v1
        kind: PersistentVolume
        metadata:
          # annotations:
            # pv.kubernetes.io/provisioned-by: block.csi.ibm.com
          name: vol1-pv
        spec:
          accessModes:
          - ReadWriteOnce
          capacity:
            storage: 1Gi
          csi:
            controllerPublishSecretRef:
              name: demo-secret
              namespace: default
            driver: block.csi.ibm.com
            # volumeAttributes:
              # pool_name: ibmc-block-gold
              # storage_type: SVC
              # volume_name: vol1
              # array_address: baremetal10-cluster.xiv.ibm.com
            volumeHandle: SVC:600507640082000B08000000000004FF
          # persistentVolumeReclaimPolicy: Retain
          storageClassName: ibmc-block-gold
          # volumeMode: Filesystem

3. Create a PersistentVolumeClaim (PVC) YAML file.

    **Note:**

    - Be sure to include the `storageClassName`.
    - For more information about creating a PVC YAML file, see [Creating a PersistentVolumeClaim (PVC)](csi_ug_config_create_pvc.md).
    
    ```
    apiVersion: v1
    kind: PersistentVolumeClaim
    metadata:
      name: vol1-pvc
    spec:
      accessModes:
      - ReadWriteOnce
      resources:
        requests:
          storage: 1Gi
      storageClassName: ibmc-block-gold
      volumeName: vol1-pv
    ```

4. Create a StatefulSet.

      For more information about creating a StatefulSet, see [Creating a StatefulSet](csi_ug_config_create_statefulset.md).