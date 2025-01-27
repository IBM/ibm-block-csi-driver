
{{site.data.keyword.attribute-definition-list}}

# Uninstalling the host definer

Use this information to uninstall the host definer.

Uninstalling the host definer does not remove any hosts from the storage, even if these hosts have been configured on storage by the host definer.{: note}

The host definer can be uninstalled in the following ways:

Be sure to use the corresponding uninstall method to match the installation method used to install the host definer.{: important}

- With the Red Hat OpenShift web console (see [Uninstalling the driver with the OpenShift web console](uninstall_driver_openshift_web.md)).
- With OperatorHub.io (see [Uninstalling the driver with OperatorHub.io](uninstall_driver_operatorhub.md)).
  
The host definer can also be uninstalled at any time with GitHub, using the following command:
  
    kubectl delete -f csi_v1_hostdefiner_cr.yaml
