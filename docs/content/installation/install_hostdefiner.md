# Installing the host definer

Install the HostDefiner custom resource to enable dynamic host definitions of the CSI driver nodes.

For information on configuring the HostDefiner custom resource, see [Configuring the host definer](../configuration/configuring_hostdefiner.md).

For more information on using dynamic host connectivity definition, see [Using dynamic host definition](../using/using_hostdefinition.md).

The host definer can be installed at any time in the following ways:

- With the Red Hat OpenShift web console (see [Installing the host definer with the OpenShift web console](install_hostdefiner_openshift_web.md)).
- With GitHub (see [Installing the host definer with GitHub](install_hostdefiner_github.md)).
-   With OperatorHub.io (see [Installing the host definer with OperatorHub.io](install_hostdefiner_operatorhub.md)).

The host definer can also be downloaded and installed as part of the operator and driver installation process. For more information, see [Installing the operator and driver](install_operator_driver.md).

After the HostDefiner custom resource, created by the operator, is installed, the HostDefiner pod automatically creates HostDefinition custom resources. The HostDefinition information can be viewed using the `kubectl get hostdefinition` command.
