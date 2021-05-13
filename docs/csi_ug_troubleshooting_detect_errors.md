# Detecting errors

Use this information to help pinpoint potential causes for stateful pod failure.

This is an overview of actions that you can take to pinpoint a potential cause for a stateful pod failure.

**Note:** This procedures is applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.

1.  Verify that the CSI driver is running. \(Make sure the `csi-controller` pod status is _Running_\).

    ```
    $> kubectl get all -n <namespace> -l csi
    ```

2.  If `pod/ibm-block-csi-controller-0` is not in a _Running_ state, run the following command:

    ```
    kubectl describe -n <namespace> pod/ibm-block-csi-controller-0
    ```

    View the logs \(see [Log collection](csi_ug_troubleshooting_logs.md)\).


