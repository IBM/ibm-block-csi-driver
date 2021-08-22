# Creating a Secret that is Topology Aware

Create an array secret YAML file in order to define the storage credentials (username and password) and address. Use this information for creating a Secret that is Topology Aware.

**Note:** If you are not using the Topology Aware feature, follow the steps in [Creating a Secret](csi_ug_config_create_secret.md).

Use the Secret file to connect a worker node to a storage system.

Use one of the following procedures to create and apply the secret:

## Creating an array secret file that is Topology Aware
1. Create the secret file, similar to the following demo-secret-config.json:

    The `management_address` field can contain more than one address, with each value separated by a comma.

    ```
        {
     "demo-system-id-1": {
       "username": "demo-username-1",
       "password": "demo-password-1",
        "management_address": "demo-management-address-1",
        "supported_topologies": [
         {
           "topology.block.csi.ibm.com/demo-region": "demo-region-1",
           "topology.block.csi.ibm.com/demo-zone": "demo-zone-1"
         }
       ]
     },
     "demo-system-id-2": {
       "username": "demo-username-2",
       "password": "demo-password-2",
       "management_address": "demo-management-address-2",
       "supported_topologies": [
         {
           "topology.block.csi.ibm.com/demo-region": "demo-region-2",
           "topology.block.csi.ibm.com/demo-zone": "demo-zone-2"
         }
       ]
     }
   }
     ```
       
2. Apply the secret using the following command:

    `kubectl apply -f demo-secret-config.json`
    

     The `secret/<NAME> created` message is emitted.


## Creating an array secret via command line
**Note:** This procedure is applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.

Create the secret using the following command:

 ```
 kubectl create secret generic <NAME> --from-literal=username=<USER> --from-literal=password=<PASSWORD>--from-literal=management_address=<ARRAY_MGMT> -n <namespace>
 ```
 
