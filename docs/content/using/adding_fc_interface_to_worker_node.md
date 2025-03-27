# Adding an FC Interface to a worker node

Follow the steps below to configure an additional interface to a worker node in a RedHat Openshift® or Kubernetes cluster.
This procedure can be useful for several reasons, for example after an HBA replacement which is not registered by Host Definer.

## Setting the stage
1. Let's assume there are 2 ports configured, but only one of them is functional. After ports are configured manually or using Host Definer, only one interface is shown as working on the storage side.

2. On the relevant worker node, the interface may be blocked, disfunctional, or possibly does not exist.
```
$ cat /sys/class/fc_host/host*/port_state
Online
Linkdown

$ cat /sys/class/fc_host/host*/port_name
0x2100f4e9d456d700
0x2100f4e9d456d701 <---------- This is the malfunctioning interface
```

3. On the storage side, the interface will be inactive.
```
IBM_FlashSystem:fab3p-63-c:superuser>lshost css-bm-04 | grep -A 2 WWPN
WWPN 2100F4E9D456D700
node_logged_in_count 2
state inactive
```

4. On the relevant worker node, when creating PVCs and PODs in this situation, only half of the paths will be available.
```
$ multipath -ll
mpathv (36005076810840239d00000000000011f) dm-0 IBM,2145
size=2.0G features='1 queue_if_no_path' hwhandler='1 alua' wp=rw
|-+- policy='service-time 0' prio=50 status=active
| `- 10:0:13:5  sdc 8:32 active ready running
`-+- policy='service-time 0' prio=10 status=enabled
  `- 10:0:11:5  sdb 8:16 active ready running
mpathw (36005076810840239d000000000000120) dm-1 IBM,2145
size=2.0G features='1 queue_if_no_path' hwhandler='1 alua' wp=rw
|-+- policy='service-time 0' prio=50 status=active
| `- 10:0:11:35 sdd 8:48 active ready running
`-+- policy='service-time 0' prio=10 status=enabled
  `- 10:0:13:35 sde 8:64 active ready running
```

5. Once the problem with the HBA resolved, the WWPN can be added manually on the storage side.

6. On the relevant worker node, when creating new PVCs and PODs, the new ones will utilize all available paths, but the old ones will remain with half of the paths.
```
$ multipath -ll
mpathv (36005076810840239d00000000000011f) dm-0 IBM,2145
size=2.0G features='1 queue_if_no_path' hwhandler='1 alua' wp=rw
|-+- policy='service-time 0' prio=50 status=active
| `- 10:0:13:5  sdc 8:32  active ready running
`-+- policy='service-time 0' prio=10 status=enabled
  `- 10:0:11:5  sdb 8:16  active ready running
mpathw (36005076810840239d000000000000120) dm-1 IBM,2145
size=2.0G features='1 queue_if_no_path' hwhandler='1 alua' wp=rw
|-+- policy='service-time 0' prio=50 status=active
| `- 10:0:11:35 sdd 8:48  active ready running
`-+- policy='service-time 0' prio=10 status=enabled
  `- 10:0:13:35 sde 8:64  active ready running
mpathx (36005076810840239d000000000000121) dm-2 IBM,2145
size=2.0G features='1 queue_if_no_path' hwhandler='1 alua' wp=rw
|-+- policy='service-time 0' prio=50 status=active
| |- 10:0:13:78 sdg 8:96  active ready running
| `- 11:0:13:78 sdi 8:128 active ready running
`-+- policy='service-time 0' prio=10 status=enabled
  |- 10:0:11:78 sdf 8:80  active ready running
  `- 11:0:11:78 sdh 8:112 active ready running
```

7. On the relevant worker node, new devices will be created only for the new volumes.
```
$ sg_map -x | grep sd
/dev/sg1  1 2 0 0  0  /dev/sda
/dev/sg17  10 0 11 5  0  /dev/sdb
/dev/sg23  10 0 13 5  0  /dev/sdc
/dev/sg55  10 0 11 35  0  /dev/sdd
/dev/sg57  10 0 13 35  0  /dev/sde
/dev/sg81  10 0 11 78  0  /dev/sdf
/dev/sg83  10 0 13 78  0  /dev/sdg
/dev/sg95  11 0 11 78  0  /dev/sdh
/dev/sg97  11 0 13 78  0  /dev/sdi
```

## Adding the missing FC Interfaces

To add the missing FC Interfaces to existing PVCs and PODs, complete the following procedure.

1. On the master node, restart the Host Definer pod and wait until it is running again

```
$ oc delete pod host-definer-hostdefiner-869849796f-zrhhv
pod "host-definer-hostdefiner-869849796f-zrhhv" deleted
$ oc get pods
NAME                                        READY   STATUS    RESTARTS   AGE
css-bm-04-debug                             1/1     Running   0          9m47s
host-definer-hostdefiner-869849796f-lklvc   1/1     Running   0          36s
ibm-block-csi-controller-0                  8/8     Running   0          9d
ibm-block-csi-node-f2s4b                    3/3     Running   0          28h
ibm-block-csi-node-wdmtt                    3/3     Running   0          9d
ibm-block-csi-node-xkcm6                    3/3     Running   0          28h
ibm-block-csi-operator-5844b8d8bb-zbvht     1/1     Running   0          28h
```

2. On the master node, confirm that the new WWPN was added to the relevant hostdefinition object:
```
$ oc get hostdefinitions
NAME                             AGE   PHASE   NODE        MANAGEMENT_ADDRESS
css-bm-04-ihkd0qfiwp8bwbny1w8u   9d    Ready   css-bm-04   9.71.254.33 <-----------
css-bm-05-uc716kiaxle7a3f6c3jt   9d    Ready   css-bm-05   9.71.254.33
css-bm-06-04xeg2gwuelt7fyo8xn2   9d    Ready   css-bm-06   9.71.254.33

$ oc describe hostdefinitions css-bm-04-ihkd0qfiwp8bwbny1w8u | tail
      2100F4E9D456D701 <------------
      2100F4E9D456D700
    Secret Name:       sec-64
    Secret Namespace:  default
Status:
  Phase:  Ready
Events:
  Type    Reason            Age   From         Message
  ----    ------            ----  ----         -------
  Normal  SuccessfulDefine  90s   hostDefiner  Host defined successfully on the array
```

3. On the relevant worker node, make sure all the paths are utilized for old multipath devices (created before the new interface was added/fixed).
```
$ rescan-scsi-bus.sh
Scanning SCSI subsystem for new devices
Scanning host 0 for  SCSI target IDs 0 1 2 3 4 5 6 7, all LUNs
 Scanning for device 0 0 0 0 ...
```

4. On the relevant worke node, confirm that previously created PVCs are connected with all available paths and sd devices are created for each one of them:
```
$ multipath -ll
mpathv (36005076810840239d00000000000011f) dm-0 IBM,2145
size=2.0G features='1 queue_if_no_path' hwhandler='1 alua' wp=rw
|-+- policy='service-time 0' prio=50 status=active
| |- 10:0:13:5  sdc 8:32  active ready running
| `- 11:0:13:5  sdl 8:176 active ready running
`-+- policy='service-time 0' prio=10 status=enabled
  |- 10:0:11:5  sdb 8:16  active ready running
  `- 11:0:11:5  sdn 8:208 active ready running
mpathw (36005076810840239d000000000000120) dm-1 IBM,2145
size=2.0G features='1 queue_if_no_path' hwhandler='1 alua' wp=rw
|-+- policy='service-time 0' prio=50 status=enabled
| |- 10:0:11:35 sdd 8:48  active ready running
| `- 11:0:11:35 sdo 8:224 active ready running
`-+- policy='service-time 0' prio=10 status=enabled
  |- 10:0:13:35 sde 8:64  active ready running
  `- 11:0:13:35 sdm 8:192 active ready running
mpathx (36005076810840239d000000000000121) dm-2 IBM,2145
size=2.0G features='1 queue_if_no_path' hwhandler='1 alua' wp=rw
|-+- policy='service-time 0' prio=50 status=active
| |- 10:0:13:78 sdg 8:96  active ready running
| `- 11:0:13:78 sdi 8:128 active ready running
`-+- policy='service-time 0' prio=10 status=enabled
  |- 10:0:11:78 sdf 8:80  active ready running
  `- 11:0:11:78 sdh 8:112 active ready running
```

