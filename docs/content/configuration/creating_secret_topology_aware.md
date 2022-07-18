# Creating a Secret with topology awareness

Create an array secret YAML file to define the storage credentials (username and password) and address. Use this information for creating a Secret that is topology aware.

**Note:** If you are not using the CSI Topology feature, follow the steps in [Creating a Secret](creating_secret.md).

Within the Secret, each user-defined management ID (here, represented by `demo-management-id-x`), is used to identify the storage system within other configuration files.

**Note:** The management ID must start and end with a character or number. In addition, the following symbols may be used within the management ID:<br>_ . -

1. Create the secret file, similar to the following `demo-secret-config.json`:

    The `management_address` field can contain more than one address, with each value separated by a comma.

        {
          "demo-management-id-1": {
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
          "demo-management-id-2": {
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
       
2. Apply the secret using the following command:

    ```
    kubectl create secret generic <secret-name> -n <secret-namespace> --from-file=config=demo-secret-config.json
    ```
    

    The `secret/<secret-name> created` message is emitted.
 
