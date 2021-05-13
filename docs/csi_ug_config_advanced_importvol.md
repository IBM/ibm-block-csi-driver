# Importing an existing volume

Use this information to import volumes created externally from the IBM® block storage CSI driver by using a persistent volume \(PV\) yaml file.

Before starting to import an existing volume, find the following information in the existing volume in order to include the information in the persistent volume \(PV\) yaml file:

-   `volumeHandle`
-   `volumeAttributes` \(optional\)

    Including:

    -   `pool_name`:_<Name of Pool where volume is located\>_ (Listed as `pool_id` for DS8000® Family systems.\)
    -   `storage_type`: <SVC \| A9K \| DS8K\>
    -   `volume_name`:_<Volume name\>_
    -   `array_address`:_<Array address\>_

To find the `volumeHandle`, use one of the following procedures:

-   **Through command line \(for Spectrum Virtualize Family\):**

    `    lsvdisk <volume name> | grep vdisk_UID`

    ```screen
    lsvdisk vol0 | grep vdisk_UID
    vdisk_UID 600507640082000B08000000000004FF
    ```

-   **Through command line \(for FlashSystem A9000 and A9000R\):**

    `vol_list_extended vol=<volume_name>`
    

    For example, for vol1:

    ```screen
    A9000>> vol_list_extended vol=vol1
    Name   WWN                                Product Serial Number     
    vol1   6001738CFC9035E8000000000091F0C0   60035E8000000000091F0C0 
    ```

-   **Through the Spectrum Virtualize management GUI:**

    1.  Select **Volumes** \> **Volumes** from the side bar.

        The **Volumes** page appears.

    2.  Browse to the volume that the port is on and right-click \> **Properties**.

        The Properties window appears. Use the UID number.

    For more information about Spectrum Virtualize products, find your product information in [IBM Documentation](https://www.ibm.com/docs/) (ibm.com/docs/\).

-   **Through the IBM Hyper-Scale Manager user interface for FlashSystem A9000 and A90000R storage systems:**

    1.  Select **Pools and Volumes Views** \> **Volumes** from the side bar.

        The **Volumes** table is displayed.

    2.  Select the `Volume`.

        The **Volume Properties** form appears.

    3.  Use the **ID** number.
    
    For more information, see [IBM Hyper-Scale Manager documentation](https://www.ibm.com/docs/en/hyper-scale-manager/) \(ibm.com/docs/en/hyper-scale-manager).


Use this procedure to help build a PV yaml file for your volumes.

**Note:** These steps are setup for importing volumes from a Spectrum Virtualize Family system. Change parameters, as needed.

1.  Create a persistent volume \(PV\) yaml file.

    **Important:** Be sure to include the `storageClassName` and `controllerPublishSecretRef` parameters or errors will occur.

2.  Take the `volume_name` and other optional information \(collected before the procedure\) and insert it into the yaml file.

    <pre>
    apiVersion: v1
    kind: PersistentVolume
    metadata:
      #annotations:
        #pv.kubernetes.io/provisioned-by: block.csi.ibm.com
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
        <b># volumeAttributes:
          \# pool_name: ibmc-block-gold
          \# storage_type: SVC
          \# volume_name: vol1
          \# array_address: baremetal10-cluster.xiv.ibm.com
        volumeHandle: SVC:600507640082000B08000000000004FF</b>
      # persistentVolumeReclaimPolicy: Retain
      storageClassName: ibmc-block-gold
      # volumeMode: Filesystem
    </pre>

3.  Create a PersistentVolumeClaim \(PVC\) yaml file.

    **Note:**

    -   To include a specific 5 Gi PV, be sure to include the `storageClassName`.
    -   For more information about creating a PVC yaml file, see [Creating a PersistentVolumeClaim \(PVC\)](csi_ug_config_create_pvc.md).
    
    ```screen
    apiVersion: v1
    kind: PersistentVolumeClaim
    metadata:
      volume.beta.kubernetes.io/storage-provisioner: block.csi.ibm.com
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

4.  Create a project namespace.

    **Using OpenShift® web console**
      From Red Hat® OpenShift Container Platform **Home** \> **Projects**, click **Create Project**. In the **Create Project** dialog box, enter a Project name \(also referred to as namespace\).

    Click **Create** to save.

    **Using command-line terminal**
    
    **Note:** This procedure is applicable for both Kubernetes and Red Hat OpenShift. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.

    Use the `kubectl create ns <namespace>` command to create a project namespace.

5.  Create a StatefulSet.

    For more information about creating a StatefulSet, see [Creating a StatefulSet](csi_ug_config_create_statefulset.md).

    ```screen
    kind: StatefulSet
    apiVersion: apps/v1
    metadata:
      name: sanity-statefulset
    spec:
      selector:
        matchLabels:
          app: sanity-statefulset
      serviceName: sanity-statefulset
      replicas: 1
      template:
        metadata:
          labels:
            app: sanity-statefulset
        spec:
          containers:
          - name: container1
            image: registry.access.redhat.com/ubi8/ubi:latest
            command: [ "/bin/sh", "-c", "--" ]
            args: [ "while true; do sleep 30; done;" ]
            volumeMounts:
              - name: vol1
                mountPath: "/data"
          volumes:
          - name: vol1
            persistentVolumeClaim:
              claimName: vol1-pvc
    
    ```


