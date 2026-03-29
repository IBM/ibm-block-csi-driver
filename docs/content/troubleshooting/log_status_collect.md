{{site.data.keyword.attribute-definition-list}}

## Detecting errors and log collection
- Use the IBM® Block CSI Driver diagnostics collection script to gather logs, events, Kubernetes/OpenShift resources, node diagnostics, storage system data, and workload context for problem determination.
- The script works on both Kubernetes and Red Hat® OpenShift® clusters and automatically selects the appropriate CLI (kubectl or oc). No additional configuration is required. {: tip}

---
## Detecting errors

To help pinpoint potential causes for stateful pod failure:

1. Verify that all CSI pods are running.
```
kubectl get pods -n <namespace> -l product=ibm-block-csi-driver
```

2. If a pod is not in a _Running_ state, run the following command and view the logs:
```
kubectl describe -n <namespace> pod/<pod-name>
```
---
## Log Collection
### Default collection
```bash
./ibm-block-csi-logs-collector.sh
```
The script can be found here: https://github.com/IBM/ibm-block-csi-driver/tree/release-1.14.0/scripts/ibm-block-csi-logs-collector.sh
* Collects: resources, logs, events, node-diagnostics
* Does not collect: storage, workload
* Scope: entire cluster
* Output: timestamped directory created automatically

---

### Help and usage information
```bash
./ibm-block-csi-logs-collector.sh -h
```
- Displays full help and usage information
- Lists all supported flags, components, behaviors, and examples
- Recommended as the first reference when running the script

---
### Diagnostics collection using the IBM Block CSI log collector script
The diagnostics script collects the following components:
* resources – CSI-related Kubernetes/OpenShift objects
* logs – CSI Node Plugin, controller, operator, and host-definer logs
* events – Cluster events, Namespace events,CSI/storage events and Warning events
* node-diagnostics – Node-level system, storage, network, and kubelet data
* storage – IBM FlashSystem / SVC storage diagnostics
* workload – Diagnostics for a specific pod and/or PVC

#### List available components:
```bash
./ibm-block-csi-logs-collector.sh --list-components
```

---
### Namespace-scoped collection
```bash
./ibm-block-csi-logs-collector.sh -n <namespace>
```
- Limits resource collection to the specified namespace where applicable
- Logs, events, and node diagnostics remain cluster-wide
- Storage system and workload diagnostics are collected only when explicitly requested

---
### Output location
```
./ibm-block-csi-logs-collector.sh -o <directory-path>
```
- Specifies where the log collection directory should be created
- The script creates: <directory-path>/ibm-block-csi-log-collection/YYYYMMDD-HHMMSS_<timezone>/
- If not specified, output is collected in the current directory

---
### Creating compressed archive
```bash
./ibm-block-csi-logs-collector.sh --zip
```
- Creates a compressed .tar.gz archive of the collected data
- Archive is created in the same location as the log collection directory
- Archive name format: ibm-block-csi-log-collection_YYYYMMDD-HHMMSS_<timezone>.tar.gz

---
### Including storage system diagnostics
```bash
./ibm-block-csi-logs-collector.sh \
  --storage-secret <secret-name> \
  --storage-secret-namespace <namespace>
```
- Collects all default components (resources, logs, events, node diagnostics)
- Adds storage system diagnostics (IBM FlashSystem / SVC)
- Workload diagnostics are collected when explicitly requested

**Flags**:
- `--storage-secret` : Name of the Kubernetes secret containing storage credentials
- `--storage-secret-namespace` : Namespace where the storage secret exists

**Note**:
- Both flags are mandatory
- If either flag is missing, storage system diagnostics are skipped

---
### Workload-specific diagnostics
- Workload diagnostics are automatically enabled when a workload target (Pod and/or PVC) is specified.
- `--namespace (-n)` is mandatory for all workload collection.
#### Pod-based workload
```bash
./ibm-block-csi-logs-collector.sh \
  -n <namespace> \
  --workload-pod <pod-name>
```
#### Collects:
- Pod YAML and describe output
- Attached PVC(s) and their describe output (if present)
- Bound PV(s) and their describe output (if present)
- Namespace events (time-filtered if time options are provided)
- CSI node plugin logs from the node hosting the pod (time-filtered if time options are provided)
- Note: If the pod has no PVCs attached, only pod, namespace events, and CSI node logs are collected

#### PVC-based workload
```bash
./ibm-block-csi-logs-collector.sh \
  -n <namespace> \
  --workload-pvc <pvc-name>
```
#### Collects:
- PVC YAML and describe output
- Bound PV YAML and describe output (if bound)
- All pods using the PVC (if any), including: Pod YAML and Pod describe output
- Namespace events (time-filtered if time options are provided)
- CSI node plugin logs from nodes hosting those pods (time-filtered if time options are provided)
- Note: If no pods are using the PVC, only PVC and PV metadata are collected

---
### Collecting component-specific data
You can control exactly which components are collected using the `--only` and `--skip` flags.

#### Collect only selected components
Use the `--only` flag with a comma-separated list of components.
```bash
./ibm-block-csi-logs-collector.sh --only <component1,component2,...>
```
**Example:**
```bash
./ibm-block-csi-logs-collector.sh --only logs,events
```
- Collects **only** `logs` and `events`
- All other components are skipped

---
#### Skip selected components
Use the `--skip` flag with a comma-separated list of components.

```bash
./ibm-block-csi-logs-collector.sh --skip <component1,component2,...>
```

**Example:**
```bash
./ibm-block-csi-logs-collector.sh --skip node-diagnostics
```
- Collects all default components except `node-diagnostics`

---

### Log size limiting
```bash
./ibm-block-csi-logs-collector.sh \
  --only logs \
  --limit-bytes <value-in-bytes>
```
- `--limit-bytes` limits the maximum size of individual log files collected
- Useful when collecting logs from long-running clusters or high-volume workloads
- The value must be specified as an integer denoting number of bytes

---

### Cluster timezone detection
- The script automatically detects the cluster timezone at startup
- All time-based operations (logs, events, kubelet journals) use this timezone
- Directory names include the detected timezone (example: 20250129-143022_IST, 20250129-143022_UTC)
- The detected timezone is stored in `COLLECTION_SUMMARY.TXT`

---

### Time-based log and event collection
- The script uses the cluster's timezone for all time filtering so time values should be provided in that timezone.
- When any time option is provided (`--since-duration`, `--start-time`, `--end-time`), filtering is applied to the following components:
    - logs: Pod logs from CSI node, CSI controller, CSI operator, and host definer
    - events: Cluster-wide, namespace-scoped, CSI/storage-related, and warning events
    - workload: Namespace events and CSI node plugin logs related to the workload
    - node-diagnostics: Kubelet journal logs

#### Using `--since-duration`
```bash
./ibm-block-csi-logs-collector.sh \
  --only logs,events \
  --since-duration <duration>
```
- `--since-duration` collects logs and events from the specified duration backward from the current time
- Examples: `30m`, `2h`, `1d`

#### Using `--start-time` and `--end-time`
```bash
./ibm-block-csi-logs-collector.sh \
  --only logs,events \
  --start-time <YYYY-MM-DDTHH:MM> \
  --end-time <YYYY-MM-DDTHH:MM>
```
- Important: Time must be specified in the cluster's timezone

#### Time filtering rules:
- `--since-duration` is mutually exclusive with `--start-time` and `--end-time`
- `--end-time`, if specified, must be accompanied by `--start-time`
- If `--start-time` is specified without `--end-time`, the `--end-time` defaults to the current cluster time

---

### Comprehensive log collection example
```bash
./ibm-block-csi-logs-collector.sh \
  -n my-app-namespace \
  -o /var/log/diagnostics \
  --storage-secret ibm-storage-secret \
  --storage-secret-namespace openshift-storage \
  --workload-pod my-app-pod \
  --workload-pvc my-pvc \
  --since-duration 4h \
  --zip
```
- Targets namespace: `my-app-namespace`
- Writes output under: `/var/log/diagnostics/ibm-block-csi-log-collection/YYYYMMDD-HHMMSS_<timezone>/`
- Collects default components: resources, logs, events, node-diagnostics
- Collects storage system diagnostics using the provided storage secret
- Collects workload diagnostics for the specified pod and PVC
- Filters logs and events from the last **4 hours** (cluster timezone)
- Creates a compressed `.tar.gz` archive in the same output location

---
### Output
#### Directory naming:
- Base directory: `ibm-block-csi-log-collection/` (created if it doesn't exist)
- Collection directory: `YYYYMMDD-HHMMSS_<timezone>/`
    - Example: `20250129-143022_UTC/` if cluster is in UTC
    - Example: `20250129-183022_IST/` if cluster is in IST

#### Compressed archive (when using `--zip`):
- Archive name: `ibm-block-csi-log-collection_YYYYMMDD-HHMMSS_<timezone>.tar.gz`
- Created in base directory

#### Summary file:
- `COLLECTION_SUMMARY.txt` is generated at the root of the collection directory
- The summary provides:
    - Collection timestamp (in cluster timezone)
    - Script version
    - Cluster type (Kubernetes or OpenShift)
    - Cluster timezone with offset (e.g., `IST (+0530)` or `UTC (+0000)`)
    - CLI used (`kubectl` or `oc`)
    - Target namespace (if specified)
    - Time filtering applied (logs & events)
    - Components collected
    - Total number of files generated
    - Absolute path to the output directory

#### Usage notes:
- If archive was created using `--zip`, share the `.tar.gz` file with IBM Support
- If no archive was created, compress the entire output directory before sharing with IBM Support
