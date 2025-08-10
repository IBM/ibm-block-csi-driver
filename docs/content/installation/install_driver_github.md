
{{site.data.keyword.attribute-definition-list}}

# Installing the driver with GitHub

The operator for IBM® block storage CSI driver can be installed directly with GitHub. Installing the CSI (Container Storage Interface) driver is part of the operator installation process.

Use the following steps to install the operator and driver, with [GitHub](https://github.com/IBM/ibm-block-csi-operator).

Before you begin, it is best practice to create a user-defined namespace. Create the project namespace, using the `kubectl create ns <namespace>` command. If a user-defined namespace was created, edit the YAML files downloaded below changing the namespace value from `default` to `<namespace>`.{: tip}

When host definer is being installed, it is preferable to do so before installing the CSI driver IBMBlockCSI custom resource (see [Installing the host definer](install_hostdefiner.md)).{: tip}

1.  Install the operator.

    1. Download the manifest from GitHub.

        ```
        curl https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/v1.12.5/deploy/installer/generated/ibm-block-csi-operator.yaml > ibm-block-csi-operator.yaml
        ```

    2.  (Optional) Update the image fields in the `ibm-block-csi-operator.yaml`.

    3. Install the operator.

        ```
        kubectl apply -f ibm-block-csi-operator.yaml
        ```

    4. Verify that the operator is running. (Make sure that the Status is _Running_.)

        ```
        $> kubectl get pod -l app.kubernetes.io/name=ibm-block-csi-operator -n <namespace>
        NAME                                    READY   STATUS    RESTARTS   AGE
        ibm-block-csi-operator-5bb7996b86-xntss 1/1     Running   0          10m
        ```

2.  Install the IBM block storage CSI driver by creating an IBMBlockCSI custom resource.

    1.  Download the manifest from GitHub.

        ```
        curl https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/v1.12.5/config/samples/csi.ibm.com_v1_ibmblockcsi_cr.yaml > csi.ibm.com_v1_ibmblockcsi_cr.yaml
        ```

    2.  (Optional) Update the image repository field, tag field, or both in the `csi.ibm.com_v1_ibmblockcsi_cr.yaml`.

    3.  Install the `csi.ibm.com_v1_ibmblockcsi_cr.yaml`.

        ```
        kubectl apply -f csi.ibm.com_v1_ibmblockcsi_cr.yaml
        ```
    
    4.  Verify that the driver is running:
        ```
        $> kubectl get pods -n <namespace> -l product=ibm-block-csi-driver
        NAME                                    READY   STATUS  RESTARTS AGE
        ibm-block-csi-controller-0              7/7     Running 0        9m36s
        ibm-block-csi-node-jvmvh                3/3     Running 0        9m36s
        ibm-block-csi-node-tsppw                3/3     Running 0        9m36s
        ibm-block-csi-operator-5bb7996b86-xntss 1/1     Running 0        10m
        ```

3. (Optional) If planning on using volume snapshots (IBM FlashCopy® function), enable support on your Kubernetes cluster.

   For more information and instructions, see the Kubernetes blog post, [Kubernetes 1.20: Kubernetes Volume Snapshot Moves to GA](https://kubernetes.io/blog/2020/12/10/kubernetes-1.20-volume-snapshot-moves-to-ga/).

   Install both the Snapshot CRDs and the Common Snapshot Controller once per cluster.

   The instructions and relevant YAML files to enable volume snapshots can be found at: [https://github.com/kubernetes-csi/external-snapshotter#usage](https://github.com/kubernetes-csi/external-snapshotter#usage)

4. (Optional) If planning on using policy-based replication with volume groups, enable support on your orchestration platform cluster and storage system.

    1. To enable support on your Kubernetes cluster, install the following replication CRDs once per cluster.

        ```
        curl -O https://raw.githubusercontent.com/IBM/csi-volume-group-operator/v0.9.2/config/crd/bases/csi.ibm.com_volumegroupclasses.yaml
        kubectl apply -f csi.ibm.com_volumegroupclasses.yaml

        curl -O https://raw.githubusercontent.com/IBM/csi-volume-group-operator/v0.9.2/config/crd/bases/csi.ibm.com_volumegroupcontents.yaml
        kubectl apply -f csi.ibm.com_volumegroupcontents.yaml

        curl -O https://raw.githubusercontent.com/IBM/csi-volume-group-operator/v0.9.2/config/crd/bases/csi.ibm.com_volumegroups.yaml
        kubectl apply -f csi.ibm.com_volumegroups.yaml
        ```

    2. Enable policy-based replication on volume groups, see the following section within your IBM Storage Virtualize® product documentation on [IBM Documentation](https://www.ibm.com/docs/): **Administering** > **Managing policy-based replication** > **Assigning replication policies to volume groups**.

5. (Optional) If planning on using volume replication (remote copy function), enable support on your orchestration platform cluster and storage system.

    1. To enable support on your Kubernetes cluster, install the following volume group CRDs once per cluster.

        ```
        curl -O https://raw.githubusercontent.com/csi-addons/volume-replication-operator/v0.3.0/config/crd/bases/replication.storage.openshift.io_volumereplicationclasses.yaml
        kubectl apply -f ./replication.storage.openshift.io_volumereplicationclasses.yaml

        curl -O https://raw.githubusercontent.com/csi-addons/volume-replication-operator/v0.3.0/config/crd/bases/replication.storage.openshift.io_volumereplications.yaml
        kubectl apply -f ./replication.storage.openshift.io_volumereplications.yaml
        ```

    2. To enable support on your storage system, see the following section within your IBM Storage Virtualize® product documentation on [IBM Documentation](https://www.ibm.com/docs/en/): **Administering** > **Managing Copy Services** > **Managing remote-copy partnerships**.

The procedures above are applicable for both Kubernetes and Red Hat OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.{: tip}
