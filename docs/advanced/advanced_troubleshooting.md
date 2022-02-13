# Troubleshooting
Use this section for advanced troubleshooting information for your CSI driver.

- [Verifying the CSI driver is running](#Verifying-the-CSI-driver-is-running)
- [Multipath troubleshooting](#Multipath-troubleshooting)
- [General troubleshooting](#General-troubleshooting)

## Verifying the CSI driver is running

  Verify that the CSI driver is running. (Make sure that all components are in the  _Running_ state), using the `$> kubectl get all -n <namespace> -l csi` command.

## Multipath troubleshooting

Use this information to help pinpoint potential causes for multipath failures.

**Note:** Run these commands should be run on one of the worker nodes.

-   **Display multipath information (FC and iSCSI)**

    Display multipath information, using the `sudo multipath -ll` command.

    <pre>
    mpathb (3600507680283851530000000000000a6) dm-0 IBM,2145
    size=1.0G features='1 queue_if_no_path' hwhandler='1 alua' wp=rw
    |-+- policy='service-time 0' prio=50 status=active
    | `- 3:0:0:0 sda 8:0  active ready running
    `-+- policy='service-time 0' prio=10 status=enabled
    `- 2:0:0:0 sdb 8:16 active ready running

-   **Display device attachment**

    Display device attachment information, using the `sudo lsblk` command.

    <pre>
    NAME     MAJ:MIN RM SIZE RO TYPE  MOUNTPOINT
    sda        8:0    0   1G  0 disk  
    `-mpathb 253:0    0   1G  0 mpath /var/lib/kubelet/pods/c9fee230-6227-11ea-a0b6-52fdfc072182/volumes/kubernetes.io~csi/pvc-32a7e21b-6227-11ea-a0b6-52fdfc
    sdb        8:16   0   1G  0 disk  
    `-mpathb 253:0    0   1G  0 mpath /var/lib/kubelet/pods/c9fee230-6227-11ea-a0b6-52fdfc072182/volumes/kubernetes.io~csi/pvc-32a7e21b-6227-11ea-a0b6-52fdfc
    vda      252:0    0  31G  0 disk  
    |-vda1   252:1    0   1M  0 part  
    |-vda2   252:2    0   1G  0 part  /boot
    `-vda3   252:3    0  30G  0 part  /sysroot
    </pre>

    To display device attachment information, together with SCSI ID information, use the `sudo lsblk -S` command.

    <pre>
    NAME HCTL       TYPE VENDOR   MODEL             REV TRAN
    sda  3:0:0:0    disk IBM      2145             0000 iscsi
    sdb  2:0:0:0    disk IBM      2145             0000 iscsi
    </pre>

-   **Check for multipath daemon availability (FC and iSCSI)**

    Check for multipath daemon availability, using the `systemctl status multipathd` command.

    <pre>
    multipathd.service - Device-Mapper Multipath Device Controller
        Loaded: loaded (/usr/lib/systemd/system/multipathd.service; enabled; vendor preset: enabled)
        Active: active (running) since Mon 2020-03-09 16:28:37 UTC; 22min ago
      Main PID: 1235 (multipathd)
        Status: "up"
        Tasks: 7
        Memory: 14.1M
            CPU: 131ms
        CGroup: /system.slice/multipathd.service
                └─1235 /sbin/multipathd -d -s
    </pre>

-   **Check for iSCSI daemon availability**

    Check for iSCSI daemon availability, using the `systemctl status iscsid` command.

    <pre>
    iscsid.service - Open-iSCSI
      Loaded: loaded (/usr/lib/systemd/system/iscsid.service; enabled; vendor preset: disabled)
      Active: active (running) since Mon 2020-03-09 16:28:37 UTC; 22min ago
        Docs: man:iscsid(8)
              man:iscsiadm(8)
    Main PID: 1440 (iscsid)
      Status: "Ready to process requests"
        Tasks: 1 (limit: 26213)
      Memory: 4.7M
          CPU: 27ms
      CGroup: /system.slice/iscsid.service
              └─1440 /usr/sbin/iscsid -f
    </pre>

## General troubleshooting

Use the following command for general troubleshooting:

    kubectl get -n <namespace>  csidriver,sa,clusterrole,clusterrolebinding,statefulset,pod,daemonset | grep ibm-block-csi

### Error during automatic iSCSI login

If an error during automatic iSCSI login occurs, perform the following steps for manual login:

**Note:** These procedures are applicable for both Kubernetes and Red Hat® OpenShift®. For Red Hat OpenShift, replace `kubectl` with `oc` in all relevant commands.

**Note:** This procedure is applicable for both RHEL and RHCOS users. When using RHCOS, use the following:

-   Log into the RHCOS node with the core user (for example, `ssh core@worker1.apps.openshift.mycluster.net`)
-   iscsiadm commands must start with sudo

1. Verify that the node.startup in the /etc/iscsi/iscsid.conf file is set to automatic. If not, set it as required and then restart iscsid (`systemctl restart iscsid`).
2. Discover and log into at least two iSCSI targets on the relevant storage systems.

    **Note:** A multipath device can't be created without at least two ports.

    <pre>
    $> iscsiadm -m discoverydb -t st -p ${STORAGE-SYSTEM-iSCSI-PORT-IP1}:3260 --discover
    $> iscsiadm -m node  -p ${STORAGE-SYSTEM-iSCSI-PORT-IP1} --login
        
    $> iscsiadm -m discoverydb -t st -p ${STORAGE-SYSTEM-iSCSI-PORT-IP2}:3260 --discover
    $> iscsiadm -m node  -p ${STORAGE-SYSTEM-iSCSI-PORT-IP2} --login

3. Verify that the login was successful and display all targets that you logged into. The portal value must be the iSCSI target IP address.

    <pre>
    $> iscsiadm -m session -R
    Rescanning session [sid: 1, target: {storage system IQN},portal: {storage system iSCSI port IP},{port number}]
    Rescanning session [sid: 2, target: {storage system IQN},portal: {storage system iSCSI port IP},{port number}]