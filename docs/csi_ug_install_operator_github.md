# Installing the driver with GitHub

The operator for IBM® block storage CSI driver can be installed directly with GitHub. Installing the CSI (Container Storage Interface) driver is part of the operator installation process.

Use the following steps to install the operator and driver, with [GitHub](https://github.com/IBM/ibm-block-csi-operator) (github.com/IBM/ibm-block-csi-operator).

**Note:** Before you begin, you may need to create a user-defined namespace. Create the project namespace, using the `kubectl create ns <namespace>` command.

1.  Install the operator.

    1. Download the manifest from GitHub.

        ```
        curl https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/v1.6.0/deploy/installer/generated/ibm-block-csi-operator.yaml > ibm-block-csi-operator.yaml
        ```

    2.  **Optional:** Update the image fields in the ibm-block-csi-operator.yaml.

    3. Install the operator, using a user-defined namespace.

        ```
        kubectl -n <namespace> apply -f ibm-block-csi-operator.yaml
        ```

    4. Verify that the operator is running. (Make sure that the Status is _Running_.)

        ```screen
        $ kubectl get pod -l app.kubernetes.io/name=ibm-block-csi-operator -n <namespace>
        NAME                                    READY   STATUS    RESTARTS   AGE
        ibm-block-csi-operator-5bb7996b86-xntss 1/1     Running   0          10m
        ```

2.  Install the IBM block storage CSI driver by creating an IBMBlockCSI custom resource.

    1.  Download the manifest from GitHub.

        ```
        curl https://raw.githubusercontent.com/IBM/ibm-block-csi-operator/v1.6.0/deploy/crds/csi.ibm.com_v1_ibmblockcsi_cr.yaml > csi.ibm.com_v1_ibmblockcsi_cr.yaml
        ```

    2.  **Optional:** Update the image repository field, tag field, or both in the csi.ibm.com_v1_ibmblockcsi_cr.yaml.

        **Note:** Updating the namespace to a user-defined namespace might be necessary in order to ensure consistency and avoid trouble with operator installation.

    3.  Install the csi.ibm.com_v1_ibmblockcsi_cr.yaml.

        ```
        kubectl -n <namespace> apply -f csi.ibm.com_v1_ibmblockcsi_cr.yaml
        ```
    
    4.  Verify that the driver is running:
        ```bash
        $ kubectl get pods -n <namespace> -l csi
        NAME READY STATUS RESTARTS AGE
        ibm-block-csi-controller-0 6/6 Running 0 9m36s
        ibm-block-csi-node-jvmvh 3/3 Running 0 9m36s
        ibm-block-csi-node-tsppw 3/3 Running 0 9m36s
        ibm-block-csi-operator-5bb7996b86-xntss 1/1 Running 0 10m
        ```


