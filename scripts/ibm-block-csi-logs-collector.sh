#!/bin/bash
# ==============================================================================
# OCP/k8s Data Collection Script 
# ==============================================================================

set -o pipefail
VERSION="3.0.0"
SCRIPT_NAME="$(basename "$0")"

# ==============================================================================
# GLOBAL CONSTANTS & COLORS
# ==============================================================================
readonly RED='\033[0;31m'
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly BLUE='\033[0;34m'
readonly NC='\033[0m'

# ==============================================================================
# STORAGE / SSH CONSTANTS
# ==============================================================================
readonly STORAGE_CONNECTION_TIMEOUT=30

readonly CSI_EVENT_REGEX='csi|ibm|block|volume(s)?|pv(c)?|'\
'persistentvolume(claim)?(s)?|volumeattachment(s)?|'\
'storageclass(es)?|volumesnapshot(class(es)?)?|'\
'volumegroup(class(es)?)?|volumereplication(class(es)?)?|'\
'attach(ed|ment)?|detach(ed|ment)?|'\
'mount(ed|ing)?|unmount(ed|ing)?|'\
'iscsi|fc|multipath'

# ==============================================================================
# GLOBAL VARIABLES
# ==============================================================================
TARGET_NAMESPACE=""
USER_START_TIME=""
USER_END_TIME=""
SINCE_DURATION=""
START_TIME=""
END_TIME=""
LOG_LIMIT_BYTES=""

TOTAL_COLLECTED=0
TOTAL_FAILED=0
TOTAL_SKIPPED=0

COLLECT_RESOURCES=true
COLLECT_LOGS=true
COLLECT_EVENTS=true
COLLECT_NODE_DIAGNOSTICS=true
COLLECT_STORAGE_SYSTEM=true
COLLECT_WORKLOAD=true

STORAGE_SECRET_NAME=""
STORAGE_SECRET_NAMESPACE=""

BASE_OUTPUT_DIR=""
KUBE_CMD=""

WORKLOAD_POD=""
WORKLOAD_PVC=""

readonly AVAILABLE_COMPONENTS=("logs" "events" "resources" "node-diagnostics" "storage" "workload")

# Temp files for cleanup
TEMP_FILES=()
TEMP_DIRS=()

# ==============================================================================
# CLEANUP HANDLER
# ==============================================================================
cleanup() {
    local exit_code=$?
    
    pkill -P $$ 2>/dev/null || true

    for file in "${TEMP_FILES[@]}"; do
        [[ -f "$file" ]] && rm -f "$file"
    done

    for dir in "${TEMP_DIRS[@]}"; do
        [[ -d "$dir" ]] && rm -rf "$dir"
    done

    unset SSH_ASKPASS SSH_ASKPASS_REQUIRE DISPLAY SSH_PASSWORD

    exit "$exit_code"
}

trap cleanup EXIT INT TERM

# ==============================================================================
# HELPER FUNCTIONS
# ==============================================================================
log_info() {
    echo -e "${BLUE}[INFO]${NC} $*"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $*"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $*"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $*"
}

print_section() {
    echo ""
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}▶ $1${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
}

print_group() { echo -e "    ${GREEN}→${NC} $1"; }
print_ok()    { echo -e "      ${GREEN}✓${NC} $1"; }
print_warn()  { echo -e "      ${YELLOW}✗${NC} $1"; }

is_openshift() {
    [[ "$KUBE_CMD" == "oc" ]]
}

show_help() {
cat << EOF
IBM Block CSI Driver Diagnostics Collection Script
=================================================

USAGE:
  ./$SCRIPT_NAME [OPTIONS]

-------------------------------------------------
GENERAL OPTIONS
-------------------------------------------------
  -h, --help
        Show this help message and exit

  -n, --namespace <namespace>
        Target a specific namespace
        Default: all namespaces

-------------------------------------------------
COMPONENT SELECTION
-------------------------------------------------
  --only <components>
        Collect only the specified components (comma-separated)

  --skip <components>
        Skip specified components (comma-separated)

  --list-components
        List all available components and exit

  Available components:
    - resources
    - logs
    - events
    - node-diagnostics
    - storage
    - workload

-------------------------------------------------
TIME FILTERING (LOGS & EVENTS ONLY)
-------------------------------------------------
  --since-duration <duration>
        Collect logs/events from the last duration
        Examples: 30m, 2h, 1d

        NOTE:
          - Cannot be used with --start-time or --end-time

  --start-time <YYYY-MM-DDTHH:MM>
        Collect logs/events starting from this time (UTC)
        Example: 2025-01-15T10:30

        NOTE:
          - Can be used alone
          - If --end-time is not specified, end time defaults to now (UTC)

  --end-time <YYYY-MM-DDTHH:MM>
        Collect logs/events up to this time (UTC)
        Example: 2025-01-15T11:45

        NOTE:
          - Must be used together with --start-time
          - Cannot be used with --since-duration

-------------------------------------------------
STORAGE SYSTEM DIAGNOSTICS (IBM FlashSystem / SVC)
-------------------------------------------------
  --storage-secret <secret-name>
        Kubernetes secret containing storage credentials

  --storage-secret-namespace <namespace>
        Namespace where the storage secret exists

        NOTE:
          - BOTH flags are required to collect storage diagnostics
          - If either is missing, storage collection is skipped

-------------------------------------------------
WORKLOAD TARGETING (WORKLOAD COMPONENT)
-------------------------------------------------
  NOTE:
    - Workload diagnostics are NOT collected by default
    - The workload component is automatically enabled when
      --workload-pod or --workload-pvc is specified
    - --namespace is REQUIRED for workload collection
    - --workload-pod and --workload-pvc CAN be used together

  --workload-pod <pod-name>
        Target a specific workload pod

        Automatically collects:
          - Pod YAML and describe output
          - Attached PVC(s) and bound PV(s), if present
          - Namespace events (time filtered)
          - CSI node plugin logs from the node hosting the pod

  --workload-pvc <pvc-name>
        Target a specific PersistentVolumeClaim (PVC)

        Automatically collects:
          - PVC YAML and describe output
          - Bound PV YAML and describe output
          - All pods using the PVC (if any)
          - Namespace events (time filtered)
          - CSI node plugin logs from nodes hosting those pods

-------------------------------------------------
LOG COLLECTION BEHAVIOR
-------------------------------------------------
  - Logs are collected with timestamps enabled, can be used with time filtering flags.
  - --limit-bytes can be used to cap log file size
  - Both current and previous container logs are collected when available

-------------------------------------------------
EVENT COLLECTION BEHAVIOR
-------------------------------------------------
  - All events are sorted by lastTimestamp & can be used with time filtering flags.
  - CSI / storage-related events are filtered using predefined keywords
  - Events are collected across ALL namespaces in 4 distinct outputs (independent of -n):
    - Cluster-wide
    - All Namespace events
    - CSI / storage-related
    - Warning events
  - Namespace scoping (-n) is applied ONLY for workload-related event collection

-------------------------------------------------
EXAMPLES
-------------------------------------------------

0) Collect DEFAULT components (no flags)
    ./$SCRIPT_NAME
   - Skips storage & workload component
   - Uses cluster-wide scope for collection

1) Collect ALL NON-WORKLOAD components (cluster-wide)
   ./$SCRIPT_NAME \\
     --storage-secret ibm-storage-secret \\
     --storage-secret-namespace openshift-storage


2) Collect EVERYTHING INCLUDING workload (pod + pvc)
   ./$SCRIPT_NAME \\
     -n my-app-namespace \\
     --storage-secret ibm-storage-secret \\
     --storage-secret-namespace openshift-storage \\
     --workload-pod my-app-pod \\
     --workload-pvc data-volume-claim

3) Collect workload diagnostics for BOTH pod and PVC
   ./$SCRIPT_NAME \\
     -n my-app-namespace \\
     --workload-pod my-app-pod \\
     --workload-pvc data-volume-claim


4) Collect ONLY workload diagnostics (nothing else)
   ./$SCRIPT_NAME \\
     -n my-app-namespace \\
     --only workload \\
     --workload-pod my-app-pod

5) Collect CSI resources in a specific namespace
   ./$SCRIPT_NAME \\
     -n openshift-storage \\
     --only resources

6) Collect LOGS from the last 2 hours
   ./$SCRIPT_NAME \\
     --only logs \\
     --since-duration 2h

7) Collect EVENTS from a specific time window
   ./$SCRIPT_NAME \\
     --only events \\
     --start-time 2025-12-21T10:30 \\
     --end-time 2025-12-21T11:30
EOF
}

check_prerequisites() {
    local missing_tools=()

    local HAS_KUBECTL=false
    local HAS_OC=false

    command -v kubectl &>/dev/null && HAS_KUBECTL=true
    command -v oc &>/dev/null && HAS_OC=true

    if [[ "$HAS_KUBECTL" != true && "$HAS_OC" != true ]]; then
        log_error "Neither 'kubectl' nor 'oc' found in PATH"
        exit 1
    fi

    if [[ "$HAS_KUBECTL" == true ]]; then
        PROBE_CMD="kubectl"
    else
        PROBE_CMD="oc"
    fi

    if ! $PROBE_CMD version --request-timeout=5s &>/dev/null; then
        log_error "Unable to access cluster"
        log_error "Reason: not logged in, invalid kubeconfig, or cluster unreachable"
        exit 1
    fi

    if $PROBE_CMD get routes.route.openshift.io --all-namespaces --no-headers >/dev/null 2>&1; then
        IS_OPENSHIFT=true
    else
        IS_OPENSHIFT=false
    fi

    if [[ "$IS_OPENSHIFT" == true && "$HAS_OC" == true ]]; then
        KUBE_CMD="oc"
    else
        KUBE_CMD="kubectl"
    fi

    local required_tools=("jq" "base64")
    for tool in "${required_tools[@]}"; do
        command -v "$tool" &>/dev/null || missing_tools+=("$tool")
    done

    if [[ "$COLLECT_STORAGE_SYSTEM" == true ]] && ! command -v ssh &>/dev/null; then
        missing_tools+=("ssh")
    fi

    if [[ ${#missing_tools[@]} -gt 0 ]]; then
        log_error "Missing required tools: ${missing_tools[*]}"
        exit 1
    fi

    log_success "Cluster type: $([[ "$IS_OPENSHIFT" == true ]] && echo OpenShift || echo Kubernetes)"
    log_success "Using Cluster CLI: $KUBE_CMD"
    log_success "Prerequisites check passed"
}

normalize_time() {
    local time="$1"
    local type="$2"
    
    if [[ ! "$time" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}$ ]]; then
        log_error "Invalid time format: $time"
        log_error "Expected format: YYYY-MM-DDTHH:MM (e.g., 2024-01-15T10:30)"
        exit 1
    fi
    
    if [[ "$type" == "start" ]]; then
        echo "${time}:00Z"
    else
        echo "${time}:59Z"
    fi
}

build_time_flags() {
    local flags="--timestamps=true"

    if [[ -n "$SINCE_DURATION" ]]; then
        flags="$flags --since=$SINCE_DURATION"
    elif [[ -n "$START_TIME" ]]; then
        flags="$flags --since-time=$START_TIME"
    fi

    if [[ -n "$LOG_LIMIT_BYTES" ]]; then
        flags="$flags --limit-bytes=$LOG_LIMIT_BYTES"
    fi

    echo "$flags"
}

build_journalctl_time_flags() {
    local flags=""

    if [[ -n "$START_TIME" ]]; then
        flags+=" --since=\"${START_TIME/T/ }\""
        flags="${flags/Z/}"
    fi

    if [[ -n "$END_TIME" ]]; then
        flags+=" --until=\"${END_TIME/T/ }\""
        flags="${flags/Z/}"
    fi

    echo "$flags"
}

filter_logs_until_time() {
    local logfile="$1"

    [[ -z "$USER_END_TIME" ]] && return

    awk -v end="$END_TIME" '
        $1 <= end { print }
    ' "$logfile" > "${logfile}.tmp" && mv "${logfile}.tmp" "$logfile"
}

filter_events_by_time() {
    local events_file="$1"
    local filtered_file="${events_file}.filtered"
    
    # If no time filtering specified, return original file
    if [[ -z "$SINCE_DURATION" && -z "$START_TIME" ]]; then
        return 0
    fi
    
    local start_filter=""
    local end_filter=""
    
    # Calculate start time for since-duration
    if [[ -n "$SINCE_DURATION" ]]; then
        # Convert duration to seconds and calculate start time
        local seconds=0
        if [[ "$SINCE_DURATION" =~ ^([0-9]+)m$ ]]; then
            seconds=$((${BASH_REMATCH[1]} * 60))
        elif [[ "$SINCE_DURATION" =~ ^([0-9]+)h$ ]]; then
            seconds=$((${BASH_REMATCH[1]} * 3600))
        elif [[ "$SINCE_DURATION" =~ ^([0-9]+)d$ ]]; then
            seconds=$((${BASH_REMATCH[1]} * 86400))
        fi
        start_filter=$(date -u -d "@$(($(date +%s) - seconds))" +%Y-%m-%dT%H:%M:%SZ)
    elif [[ -n "$START_TIME" ]]; then
        start_filter="$START_TIME"
    fi
    
    if [[ -n "$END_TIME" ]]; then
        end_filter="$END_TIME"
    fi
    
    # Build jq filter
    local jq_filter='.'
    if [[ -n "$start_filter" && -n "$end_filter" ]]; then
        jq_filter='{
            apiVersion: .apiVersion,
            kind: .kind,
            items: [
                .items[] | 
                select(
                    (.lastTimestamp // .firstTimestamp // .metadata.creationTimestamp) as $ts |
                    $ts >= "'"$start_filter"'" and $ts <= "'"$end_filter"'"
                )
            ]
        }'
    elif [[ -n "$start_filter" ]]; then
        jq_filter='{
            apiVersion: .apiVersion,
            kind: .kind,
            items: [
                .items[] | 
                select(
                    (.lastTimestamp // .firstTimestamp // .metadata.creationTimestamp) as $ts |
                    $ts >= "'"$start_filter"'"
                )
            ]
        }'
    fi
    
    # Apply filter
    if jq "$jq_filter" "$events_file" > "$filtered_file" 2>/dev/null; then
        mv "$filtered_file" "$events_file"
    else
        rm -f "$filtered_file" 2>/dev/null
    fi
}

list_components() {
    echo "Available components:"
    printf '  - %s\n' "${AVAILABLE_COMPONENTS[@]}"
}

disable_all_components() {
    COLLECT_LOGS=false
    COLLECT_EVENTS=false
    COLLECT_RESOURCES=false
    COLLECT_NODE_DIAGNOSTICS=false
    COLLECT_STORAGE_SYSTEM=false
    COLLECT_WORKLOAD=false
}

enable_component() {
    case "$1" in
        logs) COLLECT_LOGS=true ;;
        events) COLLECT_EVENTS=true ;;
        resources) COLLECT_RESOURCES=true ;;
        node-diagnostics) COLLECT_NODE_DIAGNOSTICS=true ;;
        storage) COLLECT_STORAGE_SYSTEM=true ;;
        workload) COLLECT_WORKLOAD=true ;;
        *)
            log_error "Unknown component: $1"
            exit 1
            ;;
    esac
}

disable_component() {
    case "$1" in
        logs) COLLECT_LOGS=false ;;
        events) COLLECT_EVENTS=false ;;
        resources) COLLECT_RESOURCES=false ;;
        node-diagnostics) COLLECT_NODE_DIAGNOSTICS=false ;;
        storage) COLLECT_STORAGE_SYSTEM=false ;;
        workload) COLLECT_WORKLOAD=false ;;
        *)
            log_error "Unknown component: $1"
            exit 1
            ;;
    esac
}


resource_exists() {
    local resource_type="$1"
    $KUBE_CMD get "$resource_type" --ignore-not-found=true -o name &>/dev/null
}

safe_get_resources() {
    local resource_type="$1"
    local scope="$2"  # "ns" or "cluster"
    local output
    
    if [[ "$scope" == "ns" ]] && [[ -n "$TARGET_NAMESPACE" ]]; then
        output=$($KUBE_CMD get "$resource_type" -n "$TARGET_NAMESPACE" -o json 2>/dev/null)
    else
        output=$($KUBE_CMD get "$resource_type" --all-namespaces -o json 2>/dev/null)
    fi
    
    local rc=$?
    
    if [[ $rc -eq 0 ]] && [[ -n "$output" ]] && [[ "$output" != "{}" ]] && [[ "$output" != '{"items":[]}' ]]; then
        echo "$output"
        return 0
    fi
    
    return 1
}


namespace_exists() {
    local ns="$1"
    $KUBE_CMD get namespace "$ns" &>/dev/null
}

secret_exists() {
    local secret_name="$1"
    local secret_ns="$2"
    $KUBE_CMD get secret "$secret_name" -n "$secret_ns" &>/dev/null
}

collect_resource() {
    local resource_type="$1"
    local output_file="$2"
    local cmd="$3"
    local sub_dir="$BASE_OUTPUT_DIR/resources/$resource_type"
    
    mkdir -p "$sub_dir"
    
    if eval "$cmd" > "$sub_dir/$output_file" 2>/dev/null; then
        if [[ -s "$sub_dir/$output_file" ]]; then
            echo -e "  ${GREEN}✓${NC} Collected: $resource_type/$output_file"
            ((TOTAL_COLLECTED++))
            return 0
        else
            rm -f "$sub_dir/$output_file"
            echo -e "  ${YELLOW}✗${NC} Empty: $resource_type/$output_file"
            ((TOTAL_SKIPPED++))
            return 1
        fi
    else
        rm -f "$sub_dir/$output_file" 2>/dev/null
        echo -e "  ${RED}✗${NC} Failed: $resource_type/$output_file"
        ((TOTAL_FAILED++))
        return 1
    fi
}

get_pod_node() {
    $KUBE_CMD get pod "$1" -n "$TARGET_NAMESPACE" \
        -o jsonpath='{.spec.nodeName}' 2>/dev/null
}

get_pod_pvcs() {
    $KUBE_CMD get pod "$1" -n "$TARGET_NAMESPACE" -o json | \
    jq -r '.spec.volumes[]? | select(.persistentVolumeClaim) | .persistentVolumeClaim.claimName'
}

get_pods_using_pvc() {
    local pvc="$1"

    # Step 1: find matching PVCs (exact or StatefulSet-generated)
    local pvcs
    pvcs=$($KUBE_CMD get pvc -n "$TARGET_NAMESPACE" -o json | \
        jq -r --arg pvc "$pvc" '
            .items[] |
            select(
                .metadata.name == $pvc
                or
                (.metadata.name | startswith($pvc + "-"))
            ) |
            .metadata.name
        ')

    [[ -z "$pvcs" ]] && return

    # Step 2: find pods referencing those PVCs
    $KUBE_CMD get pods -n "$TARGET_NAMESPACE" -o json | \
    jq -r --argjson pvcs "$(printf '%s\n' $pvcs | jq -R . | jq -s .)" '
        .items[] |
        select(
            .spec.volumes[]? |
            .persistentVolumeClaim? |
            .claimName as $c |
            $pvcs | index($c)
        ) |
        .metadata.name
    '
}

validate_since_duration() {
    local d="$1"

    if [[ "$d" =~ ^[1-9][0-9]*m$ ]]; then
        return 0
    elif [[ "$d" =~ ^[1-9][0-9]*h$ ]]; then
        return 0
    elif [[ "$d" =~ ^[1-9][0-9]*d$ ]]; then
        return 0
    else
        log_error "Invalid --since-duration value: '$d'"
        log_error "Valid formats are: <number>m, <number>h, <number>d"
        log_error "Examples: 30m, 2h, 1d"
        exit 1
    fi
}

collect_workloads() {
    [[ "$COLLECT_WORKLOAD" != true ]] && return

    local WORKLOAD_BASE_DIR="$BASE_OUTPUT_DIR/workload"
    mkdir -p "$WORKLOAD_BASE_DIR"

    if [[ -n "$WORKLOAD_POD" ]]; then
        WORKLOAD_DIR="$WORKLOAD_BASE_DIR/by-pod_${WORKLOAD_POD}"
        mkdir -p "$WORKLOAD_DIR"
        collect_workload_context
    fi

    if [[ -n "$WORKLOAD_PVC" ]]; then
        WORKLOAD_DIR="$WORKLOAD_BASE_DIR/by-pvc_${WORKLOAD_PVC}"
        mkdir -p "$WORKLOAD_DIR"
        collect_workload_context
    fi
}

run_common_node_diagnostics() {
    local journal_flags="$1"

    cat <<EOF
echo '=== UNAME ==='; uname -a || true
echo '=== OS_RELEASE ==='; cat /etc/os-release || true
echo '=== DMESG ==='; dmesg | tail -n 1000 || true
echo '=== TIMEDATECTL ==='; timedatectl || true

echo '=== ISCSI_SESSIONS ==='; iscsiadm -m session || true
echo '=== ISCSI_INITIATOR ==='; cat /etc/iscsi/initiatorname.iscsi || true
echo '=== MULTIPATH_LL ==='; multipath -ll || true
echo '=== MULTIPATH_CONF ==='; cat /etc/multipath.conf || true
echo '=== LSBLK ==='; lsblk || true
echo '=== MOUNTS ==='; grep -E 'csi|pv' /proc/mounts || true

echo '=== IP_ADDR ==='; ip addr || true
echo '=== IP_ROUTE ==='; ip route || true

echo '=== KUBELET_JOURNAL ==='
journalctl -u kubelet --no-pager ${journal_flags} || true
echo '=== END ==='
EOF
}

# ==============================================================================
# PHASE 1: ARGUMENT PARSING
# ==============================================================================
parse_arguments() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            -h|--help)
                show_help
                exit 0
                ;;
            -n|--namespace)
                TARGET_NAMESPACE="$2"
                shift 2
                ;;
            --only)
                disable_all_components
                IFS=',' read -ra components <<< "$2"
                for component in "${components[@]}"; do
                    enable_component "$component"
                done
                shift 2
                ;;
            --skip)
                IFS=',' read -ra components <<< "$2"
                for component in "${components[@]}"; do
                    disable_component "$component"
                done
                shift 2
                ;;
            --list-components)
                list_components
                exit 0
                ;;
            --since-duration)
                SINCE_DURATION="$2"
                shift 2
                ;;
            --start-time)
                USER_START_TIME="$2"
                shift 2
                ;;
            --end-time)
                USER_END_TIME="$2"
                shift 2
                ;;
            --limit-bytes)
                LOG_LIMIT_BYTES="$2"
                shift 2
                ;;
            --storage-secret)
                STORAGE_SECRET_NAME="$2"
                shift 2
                ;;
            --storage-secret-namespace)
                STORAGE_SECRET_NAMESPACE="$2"
                shift 2
                ;;
            --workload-pod)
                WORKLOAD_POD="$2"
                shift 2
                ;;
            --workload-pvc)
                WORKLOAD_PVC="$2"
                shift 2
                ;;
            *)
                log_error "Unknown option: $1"
                echo "Use --help for usage information"
                exit 1
                ;;
        esac
    done
}

# ==============================================================================
# PHASE 2: VALIDATION
# ==============================================================================
validate_arguments() {
    # Validate namespace if specified
    if [[ -n "$TARGET_NAMESPACE" ]]; then
        if ! namespace_exists "$TARGET_NAMESPACE"; then
            log_error "Namespace '$TARGET_NAMESPACE' does not exist"
            log_error "Available namespaces:"
            $KUBE_CMD get namespaces --no-headers -o custom-columns=":metadata.name" | head -10
            exit 1
        fi
        log_success "Target namespace '$TARGET_NAMESPACE' validated"
    fi
    
    # Validate storage secret arguments (only if storage collection is enabled)
    if [[ "$COLLECT_STORAGE_SYSTEM" == true ]]; then
        # Check if BOTH parameters are provided
        if [[ -n "$STORAGE_SECRET_NAME" ]] && [[ -n "$STORAGE_SECRET_NAMESPACE" ]]; then
            # Check if storage secret namespace exists
            if ! namespace_exists "$STORAGE_SECRET_NAMESPACE"; then
                log_error "Storage secret namespace '$STORAGE_SECRET_NAMESPACE' does not exist"
                log_error "Available namespaces:"
                $KUBE_CMD get namespaces --no-headers -o custom-columns=":metadata.name" | head -10
                exit 1
            fi
            
            # Check if secret exists in the namespace
            if ! secret_exists "$STORAGE_SECRET_NAME" "$STORAGE_SECRET_NAMESPACE"; then
                log_error "Secret '$STORAGE_SECRET_NAME' does not exist in namespace '$STORAGE_SECRET_NAMESPACE'"
                log_error "Available secrets in namespace '$STORAGE_SECRET_NAMESPACE':"
                $KUBE_CMD get secrets -n "$STORAGE_SECRET_NAMESPACE" --no-headers -o custom-columns=":metadata.name" | head -10
                exit 1
            fi
            
            log_success "Storage secret '$STORAGE_SECRET_NAME' validated in namespace '$STORAGE_SECRET_NAMESPACE'"
        fi
        # If partial info provided, it will be handled in main() display section
    fi
    
    # ------------------------------------------------------------------------------
    # Validate and normalize time options
    # ------------------------------------------------------------------------------

    # since-duration must be exclusive
    if [[ -n "$SINCE_DURATION" ]] && \
    ([[ -n "$USER_START_TIME" ]] || [[ -n "$USER_END_TIME" ]]); then
        log_error "Cannot use --since-duration together with --start-time or --end-time"
        exit 1
    fi

    # validate since-duration to fail-fast if invalid format
    if [[ -n "$SINCE_DURATION" ]]; then
        validate_since_duration "$SINCE_DURATION"
    fi

    # end-time alone is not allowed
    if [[ -z "$USER_START_TIME" ]] && [[ -n "$USER_END_TIME" ]]; then
        log_error "--end-time cannot be used without --start-time"
        exit 1
    fi

    # If start-time is provided without end-time, default end-time to NOW (UTC)
    if [[ -n "$USER_START_TIME" ]] && [[ -z "$USER_END_TIME" ]]; then
        USER_END_TIME="$(date -u +%Y-%m-%dT%H:%M)"
    fi

    # Normalize times using existing normalize_time()
    if [[ -n "$USER_START_TIME" ]]; then
        START_TIME="$(normalize_time "$USER_START_TIME" "start")"
    fi

    if [[ -n "$USER_END_TIME" ]]; then
        END_TIME="$(normalize_time "$USER_END_TIME" "end")"
    fi

    # ------------------------------------------------------------------
    # Workload targeting validation
    # ------------------------------------------------------------------

    if [[ "$COLLECT_WORKLOAD" == true ]] && \
    [[ -z "$WORKLOAD_POD" && -z "$WORKLOAD_PVC" ]]; then
        log_error "Component 'workload' requires --workload-pod or --workload-pvc"
        exit 1
    fi

    if [[ -n "$WORKLOAD_POD" || -n "$WORKLOAD_PVC" ]]; then
        if [[ -z "$TARGET_NAMESPACE" ]]; then
            log_error "--namespace is required with workload targeting"
            exit 1
        fi

        namespace_exists "$TARGET_NAMESPACE" || {
            log_error "Namespace '$TARGET_NAMESPACE' does not exist"
            exit 1
        }
    fi

    if [[ -n "$WORKLOAD_POD" ]]; then
        $KUBE_CMD get pod "$WORKLOAD_POD" -n "$TARGET_NAMESPACE" &>/dev/null || {
            log_error "Pod '$WORKLOAD_POD' not found in namespace '$TARGET_NAMESPACE'"
            exit 1
        }
    fi

    if [[ -n "$WORKLOAD_PVC" ]]; then
        $KUBE_CMD get pvc "$WORKLOAD_PVC" -n "$TARGET_NAMESPACE" &>/dev/null || {
            log_error "PVC '$WORKLOAD_PVC' not found in namespace '$TARGET_NAMESPACE'"
            exit 1
        }
    fi

}

# ==============================================================================
# PHASE 3: ENVIRONMENT SETUP
# ==============================================================================
setup_environment() {

    BASE_OUTPUT_DIR="csi-collection-$(date -u +%Y%m%d-%H%M%S)_UTC"
    mkdir -p "$BASE_OUTPUT_DIR"

    if [[ "$COLLECT_RESOURCES" == true ]]; then
        mkdir -p "$BASE_OUTPUT_DIR/resources"
    fi

    if [[ "$COLLECT_LOGS" == true ]]; then
        mkdir -p \
            "$BASE_OUTPUT_DIR/logs/node" \
            "$BASE_OUTPUT_DIR/logs/controller" \
            "$BASE_OUTPUT_DIR/logs/operator" \
            "$BASE_OUTPUT_DIR/logs/hostdefiner"
    fi

    if [[ "$COLLECT_EVENTS" == true ]]; then
        mkdir -p \
            "$BASE_OUTPUT_DIR/events/all-events" \
            "$BASE_OUTPUT_DIR/events/namespace-scoped-events" \
            "$BASE_OUTPUT_DIR/events/csi-storage-events" \
            "$BASE_OUTPUT_DIR/events/warning-events"
    fi

    if [[ "$COLLECT_NODE_DIAGNOSTICS" == true ]]; then
        mkdir -p \
            "$BASE_OUTPUT_DIR/node-diagnostics/system" \
            "$BASE_OUTPUT_DIR/node-diagnostics/storage" \
            "$BASE_OUTPUT_DIR/node-diagnostics/network" \
            "$BASE_OUTPUT_DIR/node-diagnostics/nodes" \
            "$BASE_OUTPUT_DIR/node-diagnostics/kubelet"
    fi


    if [[ "$COLLECT_STORAGE_SYSTEM" == true ]]; then
        mkdir -p "$BASE_OUTPUT_DIR/storage-system"
    fi

    log_success "Output directory created: $BASE_OUTPUT_DIR"
}

# ==============================================================================
# PHASE 4: RESOURCE COLLECTION
# ==============================================================================
collect_storage_classes() {
    log_info "Collecting StorageClasses..."
    
    local sc_json
    sc_json=$(safe_get_resources "storageclass" "cluster")
    
    if [[ $? -ne 0 ]]; then
        log_warning "No StorageClasses found"
        ((TOTAL_SKIPPED++))
        return
    fi
    
    local count=0
    while IFS= read -r sc; do
        [[ -z "$sc" ]] && continue
        collect_resource "storageclasses" "storageclass_${sc}.yaml" \
            "$KUBE_CMD get storageclass '$sc' -o yaml"
        ((count++))
    done < <(echo "$sc_json" | jq -r '.items[] | select(.provisioner=="block.csi.ibm.com") | .metadata.name' 2>/dev/null)
    
    if [[ $count -eq 0 ]]; then
        log_warning "No IBM Block CSI StorageClasses found"
        ((TOTAL_SKIPPED++))
    fi
}

collect_pvcs() {
    log_info "Collecting PersistentVolumeClaims..."
    
    local pvc_json
    pvc_json=$(safe_get_resources "pvc" "ns")
    
    if [[ $? -ne 0 ]]; then
        log_warning "No PVCs found"
        ((TOTAL_SKIPPED++))
        return
    fi
    
    local count=0
    while IFS= read -r pvc_data; do
        [[ -z "$pvc_data" ]] && continue
        
        local decoded
        decoded=$(echo "$pvc_data" | base64 --decode 2>/dev/null) || continue
        
        local ns name driver
        ns=$(echo "$decoded" | jq -r '.metadata.namespace')
        name=$(echo "$decoded" | jq -r '.metadata.name')
        driver=$(echo "$decoded" | jq -r '.metadata.annotations["volume.kubernetes.io/storage-provisioner"] // empty')
        
        if [[ "$driver" == "block.csi.ibm.com" ]]; then
            collect_resource "pvcs" "pvc_${ns}_${name}.yaml" \
                "$KUBE_CMD get pvc '$name' -n '$ns' -o yaml"
            ((count++))
        fi
    done < <(echo "$pvc_json" | jq -r '.items[] | @base64' 2>/dev/null)
    
    if [[ $count -eq 0 ]]; then
        log_warning "No IBM Block CSI PVCs found"
        ((TOTAL_SKIPPED++))
    fi
}

collect_pvs() {
    log_info "Collecting PersistentVolumes..."
    
    local pv_json
    pv_json=$(safe_get_resources "pv" "cluster")
    
    if [[ $? -ne 0 ]]; then
        log_warning "No PVs found"
        ((TOTAL_SKIPPED++))
        return
    fi
    
    local count=0
    while IFS= read -r pv; do
        [[ -z "$pv" ]] && continue
        collect_resource "pvs" "pv_${pv}.yaml" \
            "$KUBE_CMD get pv '$pv' -o yaml"
        ((count++))
    
    done < <(
        echo "$pv_json" | jq -r '
            .items[]
            | select(
                (.spec.csi.driver == "block.csi.ibm.com")
                or
                (.metadata.annotations["pv.kubernetes.io/provisioned-by"]
                    == "block.csi.ibm.com")
            )
            | .metadata.name
        ' 2>/dev/null
    )

    if [[ $count -eq 0 ]]; then
        log_warning "No IBM Block CSI PVs found"
        ((TOTAL_SKIPPED++))
    fi
}

collect_volume_snapshot_classes() {
    log_info "Collecting VolumeSnapshotClasses..."
    
    if ! resource_exists "volumesnapshotclass"; then
        log_warning "VolumeSnapshotClass CRD not installed"
        ((TOTAL_SKIPPED++))
        return
    fi
    
    local vsc_json
    vsc_json=$(safe_get_resources "volumesnapshotclass" "cluster")
    
    if [[ $? -ne 0 ]]; then
        log_warning "No VolumeSnapshotClasses found"
        ((TOTAL_SKIPPED++))
        return
    fi
    
    local count=0
    while IFS= read -r vsc; do
        [[ -z "$vsc" ]] && continue
        collect_resource "volumesnapshotclasses" "volumesnapshotclass_${vsc}.yaml" \
            "$KUBE_CMD get volumesnapshotclass '$vsc' -o yaml"
        ((count++))
    done < <(echo "$vsc_json" | jq -r '.items[] | select(.driver=="block.csi.ibm.com") | .metadata.name' 2>/dev/null)
    
    if [[ $count -eq 0 ]]; then
        log_warning "No IBM Block CSI VolumeSnapshotClasses found"
        ((TOTAL_SKIPPED++))
    fi
}

collect_volume_snapshots() {
    log_info "Collecting VolumeSnapshots..."

    if ! resource_exists "volumesnapshot"; then
        log_warning "VolumeSnapshot CRD not installed"
        ((TOTAL_SKIPPED++))
        return
    fi

    local vs_json
    vs_json=$(safe_get_resources "volumesnapshot" "ns")

    if [[ $? -ne 0 ]]; then
        log_warning "No VolumeSnapshots found"
        ((TOTAL_SKIPPED++))
        return
    fi

    local count=0
    while IFS=$'\t' read -r ns vs vsclass vsc pvc; do
        if [[ -z "$ns" || -z "$vs" ]]; then
            continue
        fi

        local driver=""

        if [[ -n "$vsclass" ]]; then
            driver=$($KUBE_CMD get volumesnapshotclass "$vsclass" -o jsonpath='{.driver}' 2>/dev/null)
        fi

        if [[ -z "$driver" && -n "$vsc" ]]; then
            driver=$($KUBE_CMD get volumesnapshotcontent "$vsc" -o jsonpath='{.spec.driver}' 2>/dev/null)
        fi

        if [[ -z "$driver" && -n "$pvc" ]]; then
            driver=$(
                $KUBE_CMD get pvc "$pvc" -n "$ns" \
                -o jsonpath='{.metadata.annotations.volume\.kubernetes\.io/storage-provisioner}' \
                2>/dev/null
            )
        fi

        if [[ "$driver" != "block.csi.ibm.com" ]]; then
            continue
        fi

        collect_resource "volumesnapshots" "volumesnapshot_${ns}_${vs}.yaml" \
            "$KUBE_CMD get volumesnapshot '$vs' -n '$ns' -o yaml"

        ((count++))
    done < <(
        echo "$vs_json" | jq -r '
            .items[]
            | [
                .metadata.namespace,
                .metadata.name,
                (.spec.volumeSnapshotClassName // ""),
                (.status.boundVolumeSnapshotContentName // ""),
                (.spec.source.persistentVolumeClaimName // "")
            ]
            | @tsv
        '
    )

    if [[ $count -eq 0 ]]; then
        log_warning "No IBM Block CSI VolumeSnapshots found"
        ((TOTAL_SKIPPED++))
    fi
}

collect_volume_snapshot_contents() {
    log_info "Collecting VolumeSnapshotContents..."
    
    if ! resource_exists "volumesnapshotcontent"; then
        log_warning "VolumeSnapshotContent CRD not installed"
        ((TOTAL_SKIPPED++))
        return
    fi
    
    local vscontent_json
    vscontent_json=$(safe_get_resources "volumesnapshotcontent" "cluster")
    
    if [[ $? -ne 0 ]]; then
        log_warning "No VolumeSnapshotContents found"
        ((TOTAL_SKIPPED++))
        return
    fi
    
    local count=0
    while IFS= read -r vscnt; do
        [[ -z "$vscnt" ]] && continue
        collect_resource "volumesnapshotcontents" "volumesnapshotcontent_${vscnt}.yaml" \
            "$KUBE_CMD get volumesnapshotcontent '$vscnt' -o yaml"
        ((count++))
    done < <(echo "$vscontent_json" | jq -r '.items[] | select(.spec.driver=="block.csi.ibm.com") | .metadata.name' 2>/dev/null)
    
    if [[ $count -eq 0 ]]; then
        log_warning "No IBM Block CSI VolumeSnapshotContents found"
        ((TOTAL_SKIPPED++))
    fi
}

collect_pods() {
    log_info "Collecting Pods..."
    
    local pod_json
    pod_json=$(safe_get_resources "pod" "ns")
    
    if [[ $? -ne 0 ]]; then
        log_warning "No Pods found"
        ((TOTAL_SKIPPED++))
        return
    fi
    
    local count=0
    while IFS=$'\t' read -r ns pod; do
        [[ -z "$ns" || -z "$pod" ]] && continue
        collect_resource "pods" "pod_${ns}_${pod}.yaml" \
            "$KUBE_CMD get pod '$pod' -n '$ns' -o yaml"
        ((count++))

    done < <(
        echo "$pod_json" | jq -r '
            .items[]
            | select(
                .metadata.labels.product == "ibm-block-csi-driver"
            )
            | [
                .metadata.namespace,
                .metadata.name
            ]
            | @tsv
        ' 2>/dev/null
    )
    
    if [[ $count -eq 0 ]]; then
        log_warning "No IBM Block CSI Pods found"
        ((TOTAL_SKIPPED++))
    fi
}

collect_daemonsets() {
    log_info "Collecting DaemonSets..."
    
    local ds_json
    ds_json=$(safe_get_resources "daemonset" "ns")
    
    if [[ $? -ne 0 ]]; then
        log_warning "No DaemonSets found"
        ((TOTAL_SKIPPED++))
        return
    fi
    
    local count=0
    while IFS=$'\t' read -r ns ds; do
        [[ -z "$ns" || -z "$ds" ]] && continue
        collect_resource "daemonsets" "daemonset_${ns}_${ds}.yaml" \
            "$KUBE_CMD get daemonset '$ds' -n '$ns' -o yaml"
        ((count++))

    done < <(
        echo "$ds_json" | jq -r '
            .items[]
            | select(
                .metadata.labels.product == "ibm-block-csi-driver"
            )
            | [
                .metadata.namespace,
                .metadata.name
            ]
            | @tsv
        ' 2>/dev/null
    )
    
    if [[ $count -eq 0 ]]; then
        log_warning "No IBM Block CSI DaemonSets found"
        ((TOTAL_SKIPPED++))
    fi
}

collect_deployments() {
    log_info "Collecting Deployments..."
    
    local deploy_json
    deploy_json=$(safe_get_resources "deployment" "ns")
    
    if [[ $? -ne 0 ]]; then
        log_warning "No Deployments found"
        ((TOTAL_SKIPPED++))
        return
    fi
    
    local count=0
    while IFS=$'\t' read -r ns deploy; do
        [[ -z "$ns" || -z "$deploy" ]] && continue
        collect_resource "deployments" "deployment_${ns}_${deploy}.yaml" \
            "$KUBE_CMD get deployment '$deploy' -n '$ns' -o yaml"
        ((count++))
    
    done < <(
        echo "$deploy_json" | jq -r '
            .items[]
            | select(
                .metadata.labels.product == "ibm-block-csi-driver"
            )
            | [
                .metadata.namespace,
                .metadata.name
            ]
            | @tsv
        ' 2>/dev/null
    )
    
    if [[ $count -eq 0 ]]; then
        log_warning "No IBM Block CSI Deployments found"
        ((TOTAL_SKIPPED++))
    fi
}

collect_services() {
    log_info "Collecting Services..."
    
    local svc_json
    svc_json=$(safe_get_resources "service" "ns")
    
    if [[ $? -ne 0 ]]; then
        log_warning "No Services found"
        ((TOTAL_SKIPPED++))
        return
    fi
    
    local count=0
    while IFS=$'\t' read -r ns svc; do
        [[ -z "$ns" || -z "$svc" ]] && continue
        collect_resource "services" "service_${ns}_${svc}.yaml" \
            "$KUBE_CMD get service '$svc' -n '$ns' -o yaml"
        ((count++))

    done < <(
        echo "$svc_json" | jq -r '
            .items[]
            | select(
                .metadata.labels.product == "ibm-block-csi-driver"
            )
            | [
                .metadata.namespace,
                .metadata.name
            ]
            | @tsv
        ' 2>/dev/null
    )

    if [[ $count -eq 0 ]]; then
        log_warning "No IBM Block CSI Services found"
        ((TOTAL_SKIPPED++))
    fi
}

collect_crds() {
    log_info "Collecting CRDs..."
    
    local crd_json
    crd_json=$(safe_get_resources "crd" "cluster")
    
    if [[ $? -ne 0 ]]; then
        log_warning "No CRDs found"
        ((TOTAL_SKIPPED++))
        return
    fi
    
    local count=0
    while IFS= read -r crd; do
        [[ -z "$crd" ]] && continue
        collect_resource "crds" "crd_${crd}.yaml" \
            "$KUBE_CMD get crd '$crd' -o yaml"
        ((count++))
    done < <(echo "$crd_json" | jq -r '.items[] | select(.metadata.name | endswith("csi.ibm.com")) | .metadata.name' 2>/dev/null)
    
    if [[ $count -eq 0 ]]; then
        log_warning "No IBM Block CSI CRDs found"
        ((TOTAL_SKIPPED++))
    fi
}

collect_csvs() {
    # Only collect CSVs on OpenShift
    if [[ "$KUBE_CMD" != "oc" ]] || ! resource_exists "csv"; then
        log_warning "K8s Cluster or No IBM Block CSI CSVs found"
        return
    fi
    
    log_info "Collecting ClusterServiceVersions..."
    
    local csv_json
    csv_json=$(safe_get_resources "csv" "ns")
    
    if [[ $? -ne 0 ]]; then
        log_warning "No CSVs found"
        ((TOTAL_SKIPPED++))
        return
    fi
    
    local count=0
    while IFS=$'\t' read -r ns csv; do
        [[ -z "$ns" || -z "$csv" ]] && continue
        collect_resource "csvs" "csv_${ns}_${csv}.yaml" \
            "$KUBE_CMD get csv '$csv' -n '$ns' -o yaml"
        ((count++))

    done < <(
        echo "$csv_json" | jq -r '
            .items[]
            | select(
                .metadata.name
                | contains("ibm-block-csi")
            )
            | [
                .metadata.namespace,
                .metadata.name
            ]
            | @tsv
        ' 2>/dev/null
    )

    if [[ $count -eq 0 ]]; then
        log_warning "No IBM Block CSI CSVs found"
        ((TOTAL_SKIPPED++))
    fi
}

collect_volume_group_classes() {
    log_info "Collecting VolumeGroupClasses..."

    if ! resource_exists "volumegroupclass"; then
        log_warning "VolumeGroupClass CRD not installed"
        ((TOTAL_SKIPPED++))
        return
    fi

    local vgclass_json
    vgclass_json=$(safe_get_resources "volumegroupclass" "cluster") || {
        log_warning "No VolumeGroupClasses found"
        ((TOTAL_SKIPPED++))
        return
    }

    local count=0
    while IFS= read -r vgclass; do
        [[ -z "$vgclass" ]] && continue
        collect_resource "volumegroupclasses" "volumegroupclass_${vgclass}.yaml" \
            "$KUBE_CMD get volumegroupclass '$vgclass' -o yaml"
        ((count++))
    done < <(echo "$vgclass_json" | jq -r '.items[] | select(.driver=="block.csi.ibm.com") | .metadata.name' 2>/dev/null)

    [[ $count -eq 0 ]] && log_warning "No IBM Block CSI VolumeGroupClasses found" && ((TOTAL_SKIPPED++))
}

collect_volume_groups() {
    log_info "Collecting VolumeGroups..."

    if ! resource_exists "volumegroup"; then
        log_warning "VolumeGroup CRD not installed"
        ((TOTAL_SKIPPED++))
        return
    fi

    local vg_json
    vg_json=$(safe_get_resources "volumegroup" "ns") || {
        log_warning "No VolumeGroups found"
        ((TOTAL_SKIPPED++))
        return
    }

    local count=0
    while IFS=$'\t' read -r ns vg vgclass vgc; do
        [[ -z "$ns" || -z "$vg" ]] && continue

        local driver=""
        [[ -n "$vgclass" ]] && \
            driver=$($KUBE_CMD get volumegroupclass "$vgclass" -o jsonpath='{.driver}' 2>/dev/null)

        [[ -z "$driver" && -n "$vgc" ]] && \
            driver=$($KUBE_CMD get volumegroupcontent "$vgc" -n "$ns" -o jsonpath='{.spec.source.driver}' 2>/dev/null)

        if [[ "$driver" == "block.csi.ibm.com" ]]; then
            collect_resource "volumegroups" "volumegroup_${ns}_${vg}.yaml" \
                "$KUBE_CMD get volumegroup '$vg' -n '$ns' -o yaml"
            ((count++))
        fi
    done < <(
        echo "$vg_json" | jq -r '
            .items[]
            | [
                .metadata.namespace,
                .metadata.name,
                (.spec.volumeGroupClassName // ""),
                (.status.boundVolumeGroupContentName // "")
            ]
            | @tsv
        '
    )

    [[ $count -eq 0 ]] && log_warning "No IBM Block CSI VolumeGroups found" && ((TOTAL_SKIPPED++))
}

collect_volume_replication_classes() {
    log_info "Collecting VolumeReplicationClasses..."

    if ! resource_exists "volumereplicationclass"; then
        log_warning "VolumeReplicationClass CRD not installed"
        ((TOTAL_SKIPPED++))
        return
    fi

    local vrc_json
    vrc_json=$(safe_get_resources "volumereplicationclass" "cluster") || {
        log_warning "No VolumeReplicationClasses found"
        ((TOTAL_SKIPPED++))
        return
    }

    local count=0
    while IFS= read -r vrc; do
        [[ -z "$vrc" ]] && continue
        collect_resource "volumereplicationclasses" "volumereplicationclass_${vrc}.yaml" \
            "$KUBE_CMD get volumereplicationclass '$vrc' -o yaml"
        ((count++))
    done < <(echo "$vrc_json" | jq -r '.items[] | select(.spec.provisioner=="block.csi.ibm.com") | .metadata.name' 2>/dev/null)

    [[ $count -eq 0 ]] && log_warning "No IBM Block CSI VolumeReplicationClasses found" && ((TOTAL_SKIPPED++))
}

collect_volume_replications() {
    log_info "Collecting VolumeReplications..."

    if ! resource_exists "volumereplication"; then
        log_warning "VolumeReplication CRD not installed"
        ((TOTAL_SKIPPED++))
        return
    fi

    local vr_json
    vr_json=$(safe_get_resources "volumereplication" "ns") || {
        log_warning "No VolumeReplications found"
        ((TOTAL_SKIPPED++))
        return
    }

    local count=0
    while IFS=$'\t' read -r ns vr vrc ds_kind ds_name; do
        [[ -z "$ns" || -z "$vr" ]] && continue

        local driver=""

        [[ -n "$vrc" ]] && \
            driver=$($KUBE_CMD get volumereplicationclass "$vrc" -o jsonpath='{.spec.provisioner}' 2>/dev/null)

        if [[ -z "$driver" && "$ds_kind" == "VolumeGroup" ]]; then
            local vgclass vgc
            vgclass=$($KUBE_CMD get volumegroup "$ds_name" -n "$ns" -o jsonpath='{.spec.volumeGroupClassName}' 2>/dev/null)
            [[ -n "$vgclass" ]] && \
                driver=$($KUBE_CMD get volumegroupclass "$vgclass" -o jsonpath='{.driver}' 2>/dev/null)

            [[ -z "$driver" ]] && {
                vgc=$($KUBE_CMD get volumegroup "$ds_name" -n "$ns" -o jsonpath='{.status.boundVolumeGroupContentName}' 2>/dev/null)
                [[ -n "$vgc" ]] && \
                    driver=$($KUBE_CMD get volumegroupcontent "$vgc" -n "$ns" -o jsonpath='{.spec.source.driver}' 2>/dev/null)
            }
        fi

        if [[ "$driver" == "block.csi.ibm.com" ]]; then
            collect_resource "volumereplications" "volumereplication_${ns}_${vr}.yaml" \
                "$KUBE_CMD get volumereplication '$vr' -n '$ns' -o yaml"
            ((count++))
        fi
    done < <(
        echo "$vr_json" | jq -r '
            .items[]
            | [
                .metadata.namespace,
                .metadata.name,
                (.spec.volumeReplicationClass // ""),
                (.spec.dataSource.kind // ""),
                (.spec.dataSource.name // "")
            ]
            | @tsv
        '
    )

    [[ $count -eq 0 ]] && log_warning "No IBM Block CSI VolumeReplications found" && ((TOTAL_SKIPPED++))
}

collect_replicasets() {
    log_info "Collecting ReplicaSets..."

    local rs_json
    rs_json=$(safe_get_resources "replicaset" "ns") || {
        log_warning "No ReplicaSets found"
        ((TOTAL_SKIPPED++))
        return
    }

    local count=0
    while IFS=$'\t' read -r ns rs; do
        [[ -z "$ns" || -z "$rs" ]] && continue
        collect_resource "replicasets" "replicaset_${ns}_${rs}.yaml" \
            "$KUBE_CMD get replicaset '$rs' -n '$ns' -o yaml"
        ((count++))
    
    done < <(
        echo "$rs_json" | jq -r '
            .items[]
            | select(
                .metadata.labels.product
                == "ibm-block-csi-driver"
            )
            | [
                .metadata.namespace,
                .metadata.name
            ]
            | @tsv
        ' 2>/dev/null
    )

    [[ $count -eq 0 ]] && log_warning "No IBM Block CSI ReplicaSets found" && ((TOTAL_SKIPPED++))
}

collect_statefulsets() {
    log_info "Collecting StatefulSets..."

    local sts_json
    sts_json=$(safe_get_resources "statefulset" "ns") || {
        log_warning "No StatefulSets found"
        ((TOTAL_SKIPPED++))
        return
    }

    local count=0
    while IFS=$'\t' read -r ns sts; do
        [[ -z "$ns" || -z "$sts" ]] && continue
        collect_resource "statefulsets" "statefulset_${ns}_${sts}.yaml" \
            "$KUBE_CMD get statefulset '$sts' -n '$ns' -o yaml"
        ((count++))

    done < <(
        echo "$sts_json" | jq -r '
            .items[]
            | select(
                .metadata.labels.product
                == "ibm-block-csi-driver"
            )
            | [
                .metadata.namespace,
                .metadata.name
            ]
            | @tsv
        ' 2>/dev/null
    )

    [[ $count -eq 0 ]] && log_warning "No IBM Block CSI StatefulSets found" && ((TOTAL_SKIPPED++))
}

collect_volume_group_contents() {
    log_info "Collecting VolumeGroupContents..."

    if ! resource_exists "volumegroupcontent"; then
        log_warning "VolumeGroupContent CRD not installed"
        ((TOTAL_SKIPPED++))
        return
    fi

    local vgc_json
    vgc_json=$(safe_get_resources "volumegroupcontent" "ns") || {
        log_warning "No VolumeGroupContents found"
        ((TOTAL_SKIPPED++))
        return
    }

    local count=0
    while IFS=$'\t' read -r ns vgc driver; do
        [[ -z "$ns" || -z "$vgc" ]] && continue

        if [[ "$driver" == "block.csi.ibm.com" ]]; then
            collect_resource "volumegroupcontents" \
                "volumegroupcontent_${ns}_${vgc}.yaml" \
                "$KUBE_CMD get volumegroupcontent '$vgc' -n '$ns' -o yaml"
            ((count++))
        fi
    done < <(
        echo "$vgc_json" | jq -r '
            .items[]
            | [
                .metadata.namespace,
                .metadata.name,
                (.spec.source.driver // "")
            ]
            | @tsv
        '
    )

    [[ $count -eq 0 ]] && log_warning "No IBM Block CSI VolumeGroupContents found" && ((TOTAL_SKIPPED++))
}

collect_all_resources() {
    if [[ "$COLLECT_RESOURCES" != true ]]; then
        return
    fi

    print_section "Collecting K8s/OC Resources"

    collect_crds
    collect_csvs
    collect_storage_classes
    collect_volume_snapshot_classes
    collect_volume_snapshots
    collect_volume_snapshot_contents
    collect_volume_group_classes
    collect_volume_groups
    collect_volume_group_contents
    collect_volume_replication_classes
    collect_volume_replications
    collect_pvs
    collect_pvcs
    collect_statefulsets
    collect_deployments
    collect_replicasets
    collect_daemonsets
    collect_pods
    collect_services
}

# ==============================================================================
# PHASE 5: EVENTS COLLECTION
# ==============================================================================
collect_events() {
    [[ "$COLLECT_EVENTS" != true ]] && return

    print_section "Collecting K8s/OC Events"

    # --------------------------------------------------------------------------
    # 1. ALL EVENTS (CLUSTER-WIDE)
    # --------------------------------------------------------------------------
    log_info "Collecting cluster-wide events..."

    mkdir -p "$BASE_OUTPUT_DIR/events/all-events"

    # 1.1 Raw table output
    $KUBE_CMD get events -A \
        > "$BASE_OUTPUT_DIR/events/all-events/all-events-overview-table.txt" 2>/dev/null

    # 1.2 JSON -> time filter -> YAML
    $KUBE_CMD get events -A \
        --sort-by='.lastTimestamp' \
        -o json \
        > "$BASE_OUTPUT_DIR/events/all-events/all-events.json" 2>/dev/null

    if [[ -s "$BASE_OUTPUT_DIR/events/all-events/all-events.json" ]]; then
        filter_events_by_time "$BASE_OUTPUT_DIR/events/all-events/all-events.json"

        jq -r '.' "$BASE_OUTPUT_DIR/events/all-events/all-events.json" | \
            $KUBE_CMD apply --dry-run=client -f - -o yaml \
            > "$BASE_OUTPUT_DIR/events/all-events/all-events-data.yaml" 2>/dev/null

        rm -f "$BASE_OUTPUT_DIR/events/all-events/all-events.json"
        log_success "Cluster-wide events collected"
    else
        log_warning "No cluster-wide events found"
        rm -f "$BASE_OUTPUT_DIR/events/all-events/all-events.json"
    fi

    # --------------------------------------------------------------------------
    # 2. NAMESPACE-SCOPED EVENTS
    # --------------------------------------------------------------------------
    log_info "Collecting namespace-scoped events..."

    mkdir -p "$BASE_OUTPUT_DIR/events/namespace-scoped-events"

    local namespaces
    namespaces=$($KUBE_CMD get namespaces -o jsonpath='{.items[*].metadata.name}')

    for ns in $namespaces; do
        # Fetch events JSON once
        events_json=$($KUBE_CMD get events -n "$ns" -o json 2>/dev/null)

        # Skip namespace if there are ZERO events
        if [[ -z "$events_json" ]] || \
        [[ "$(echo "$events_json" | jq '.items | length')" -eq 0 ]]; then
            continue
        fi

        # Create directory ONLY if events exist
        local ns_dir="$BASE_OUTPUT_DIR/events/namespace-scoped-events/$ns"
        mkdir -p "$ns_dir"

        # 1. Raw table output
        $KUBE_CMD get events -n "$ns" \
            > "$ns_dir/${ns}-events-overview-table.txt" 2>/dev/null

        # 2. JSON → time filter → YAML
        echo "$events_json" > "$ns_dir/events.json"

        filter_events_by_time "$ns_dir/events.json"

        jq -r '.' "$ns_dir/events.json" | \
            $KUBE_CMD apply --dry-run=client -f - -o yaml \
            > "$ns_dir/${ns}-events-data.yaml" 2>/dev/null

        rm -f "$ns_dir/events.json"
    done

    log_success "Namespace-scoped events collected"


    # --------------------------------------------------------------------------
    # 3. CSI / STORAGE EVENTS
    # --------------------------------------------------------------------------
    log_info "Filtering CSI/storage-related events..."

    mkdir -p "$BASE_OUTPUT_DIR/events/csi-storage-events"

    $KUBE_CMD get events -A \
        --sort-by='.lastTimestamp' \
        -o json 2>/dev/null | \
    jq --arg re "$CSI_EVENT_REGEX" '
    {
        apiVersion: "v1",
        kind: "EventList",
        items: [
            .items[] |
            select(
                (.message // "" | test($re; "i")) or
                (.reason // "" | test($re; "i")) or
                (.reportingComponent // "" | test($re; "i")) or
                (.involvedObject.kind // "" | test($re; "i")) or
                (.involvedObject.name // "" | test($re; "i"))
            )
        ]
    } | select(.items | length > 0)
    ' > "$BASE_OUTPUT_DIR/events/csi-storage-events/csi-storage-events-data.json" 2>/dev/null

    if [[ -s "$BASE_OUTPUT_DIR/events/csi-storage-events/csi-storage-events-data.json" ]]; then
        filter_events_by_time "$BASE_OUTPUT_DIR/events/csi-storage-events/csi-storage-events-data.json"
        log_success "CSI/storage events collected"
    else
        log_warning "No CSI/storage-related events found"
        rm -f "$BASE_OUTPUT_DIR/events/csi-storage-events/csi-storage-events-data.json"
    fi

    # --------------------------------------------------------------------------
    # 4. WARNING EVENTS
    # --------------------------------------------------------------------------
    log_info "Collecting warning events..."

    mkdir -p "$BASE_OUTPUT_DIR/events/warning-events"

    # 4.1 Raw table output
    $KUBE_CMD get events -A \
        --field-selector type=Warning \
        > "$BASE_OUTPUT_DIR/events/warning-events/warning-events-overview-table.txt" 2>/dev/null

    # 4.2 JSON -> time filter -> YAML
    $KUBE_CMD get events -A \
        --field-selector type=Warning \
        -o json \
        > "$BASE_OUTPUT_DIR/events/warning-events/warning-events.json" 2>/dev/null

    if [[ -s "$BASE_OUTPUT_DIR/events/warning-events/warning-events.json" ]]; then
        filter_events_by_time "$BASE_OUTPUT_DIR/events/warning-events/warning-events.json"

        jq -r '.' "$BASE_OUTPUT_DIR/events/warning-events/warning-events.json" | \
            $KUBE_CMD apply --dry-run=client -f - -o yaml \
            > "$BASE_OUTPUT_DIR/events/warning-events/warning-events-data.yaml" 2>/dev/null

        rm -f "$BASE_OUTPUT_DIR/events/warning-events/warning-events.json"
        log_success "Warning events collected"
    else
        log_warning "No warning events found"
        rm -f "$BASE_OUTPUT_DIR/events/warning-events/warning-events.json"
    fi
}

# ==============================================================================
# PHASE 6: LOGS COLLECTION
# ==============================================================================
collect_pod_logs() {
    local pod="$1"
    local namespace="$2"
    local output_dir="$3"
    local log_flags="$4"

    log_info "Collecting logs for pod: $pod"

    local containers
    containers=$($KUBE_CMD get pod "$pod" -n "$namespace" \
        -o jsonpath='{.spec.containers[*].name}' 2>/dev/null)

    if [[ -z "$containers" ]]; then
        log_warning "No containers found in pod $pod"
        return
    fi

    for container in $containers; do
        echo -e "    ${GREEN}→${NC} Container: $container"

        local logfile="$output_dir/${pod}_${container}.log"

        # Current logs
        if $KUBE_CMD logs "$pod" -n "$namespace" -c "$container" $log_flags \
            > "$logfile" 2>/dev/null; then

            filter_logs_until_time "$logfile"
            echo -e "      ${GREEN}✓${NC} Current log saved"
        else
            echo -e "      ${YELLOW}✗${NC} Failed to collect current log"
        fi

        # Previous logs
        local prevlog="$output_dir/${pod}_${container}.previous.log"

        if $KUBE_CMD logs --previous "$pod" -n "$namespace" -c "$container" $log_flags \
            > "$prevlog" 2>/dev/null; then

            filter_logs_until_time "$prevlog"
            echo -e "      ${GREEN}✓${NC} Previous log saved"
        else
            rm -f "$prevlog" 2>/dev/null
        fi
    done
}

collect_logs() {
    if [[ "$COLLECT_LOGS" != true ]]; then
        return
    fi
    
    print_section "Collecting Pod Logs"
    
    local log_flags
    log_flags=$(build_time_flags "log")
    
    # CSI Node Plugin logs
    log_info "Collecting CSI Node Plugin logs..."
    local node_pods
    node_pods=$($KUBE_CMD get pods --all-namespaces \
        -l product=ibm-block-csi-driver \
        -l app.kubernetes.io/component=csi-node \
        -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.metadata.namespace}{"\n"}{end}' 2>/dev/null)
    
    if [[ -n "$node_pods" ]]; then
        while read -r pod namespace; do
            [[ -z "$pod" ]] && continue
            collect_pod_logs "$pod" "$namespace" "$BASE_OUTPUT_DIR/logs/node" "$log_flags"
        done <<< "$node_pods"
    else
        log_warning "No CSI node pods found"
    fi
    
    # CSI Controller logs
    log_info "Collecting CSI Controller logs..."
    local controller_info
    controller_info=$($KUBE_CMD get pods --all-namespaces \
        -l product=ibm-block-csi-driver \
        -l app.kubernetes.io/component=csi-controller \
        -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.metadata.namespace}{"\n"}{end}' 2>/dev/null)

    if [[ -n "$controller_info" ]]; then
        while read -r pod namespace; do
            [[ -z "$pod" ]] && continue
            collect_pod_logs "$pod" "$namespace" "$BASE_OUTPUT_DIR/logs/controller" "$log_flags"
        done <<< "$controller_info"
    else
        log_warning "No CSI controller pod found"
    fi
    
    # CSI Operator logs
    log_info "Collecting CSI Operator logs..."
    local operator_info
    operator_info=$($KUBE_CMD get pods --all-namespaces \
        -l app.kubernetes.io/name=ibm-block-csi-operator \
        -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.metadata.namespace}{"\n"}{end}' 2>/dev/null)
    
    if [[ -n "$operator_info" ]]; then
        while read -r pod namespace; do
            [[ -z "$pod" ]] && continue
            collect_pod_logs "$pod" "$namespace" "$BASE_OUTPUT_DIR/logs/operator" "$log_flags"
        done <<< "$operator_info"
    else
        log_warning "No CSI operator pod found"
    fi
    
    # Host Definer logs
    log_info "Collecting Host Definer logs..."
    local hostdefiner_info
    hostdefiner_info=$($KUBE_CMD get pods --all-namespaces \
        -l app.kubernetes.io/component=hostdefiner \
        -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.metadata.namespace}{"\n"}{end}' 2>/dev/null)
    
    if [[ -n "$hostdefiner_info" ]]; then
        while read -r pod namespace; do
            [[ -z "$pod" ]] && continue
            collect_pod_logs "$pod" "$namespace" "$BASE_OUTPUT_DIR/logs/hostdefiner" "$log_flags"
        done <<< "$hostdefiner_info"
    else
        log_warning "No host definer pod found"
    fi
}

# ==============================================================================
# PHASE 7: NODE DIAGNOSTICS
# ==============================================================================
collect_node_diagnostics() {
    [[ "$COLLECT_NODE_DIAGNOSTICS" != true ]] && return

    print_section "Collecting Node Diagnostics"

    log_info "Finding nodes..."
    local nodes
    nodes=$($KUBE_CMD get nodes -o jsonpath='{.items[*].metadata.name}' 2>/dev/null)

    [[ -z "$nodes" ]] && {
        log_warning "No nodes found"
        return
    }

    local kubelet_journal_flags
    kubelet_journal_flags="$(build_journalctl_time_flags)"

    for node in $nodes; do
        log_info "Processing node: $node"

        # ------------------------------------------------------------------
        # Node YAML
        # ------------------------------------------------------------------
        print_group "Collecting node YAML..."
        if $KUBE_CMD get node "$node" -o yaml \
            > "$BASE_OUTPUT_DIR/node-diagnostics/nodes/node_${node}.yaml" 2>/dev/null; then
            print_ok "Node YAML saved"
        else
            print_warn "Node YAML failed"
        fi

        # ------------------------------------------------------------------
        # Node Describe
        # ------------------------------------------------------------------
        print_group "Collecting node description..."
        if $KUBE_CMD describe node "$node" \
            > "$BASE_OUTPUT_DIR/node-diagnostics/nodes/node_${node}_describe.txt" 2>/dev/null; then
            print_ok "Node describe saved"
        else
            print_warn "Node describe failed"
        fi

        local tmp_out
        tmp_out="$(mktemp)"
        TEMP_FILES+=("$tmp_out")

        print_group "Starting debug session..."
        print_group "Gathering kubelet logs this could take some time. Please wait..."

        if is_openshift; then
            if ! $KUBE_CMD debug "node/$node" --quiet < /dev/null <<EOF > "$tmp_out" 2>&1
chroot /host
$(run_common_node_diagnostics "$kubelet_journal_flags")
exit
EOF
            then
                print_warn "OpenShift debug session failed"
                echo ""
                continue
            fi
        else
            if ! $KUBE_CMD debug "node/$node" \
                -it \
                --profile=sysadmin \
                --image=registry.access.redhat.com/ubi9/ubi:latest \
                -- chroot /host /bin/sh -c "
$(run_common_node_diagnostics "$kubelet_journal_flags")
exit 0
" > "$tmp_out" 2>&1; then
                print_warn "Kubernetes debug session failed"
                echo ""
                continue
            fi
        fi

        # ------------------------------------------------------------------
        # System
        # ------------------------------------------------------------------
        print_group "Collecting system information..."

        awk '/=== UNAME ===/,/=== OS_RELEASE ===/' "$tmp_out" | sed '1d;$d' \
            > "$BASE_OUTPUT_DIR/node-diagnostics/system/${node}_uname.txt"

        awk '/=== OS_RELEASE ===/,/=== DMESG ===/' "$tmp_out" | sed '1d;$d' \
            > "$BASE_OUTPUT_DIR/node-diagnostics/system/${node}_os-release.txt"

        awk '/=== DMESG ===/,/=== TIMEDATECTL ===/' "$tmp_out" | sed '1d;$d' \
            > "$BASE_OUTPUT_DIR/node-diagnostics/system/${node}_dmesg.txt"

        awk '/=== TIMEDATECTL ===/,/=== ISCSI_SESSIONS ===/' "$tmp_out" | sed '1d;$d' \
            > "$BASE_OUTPUT_DIR/node-diagnostics/system/${node}_timedatectl.txt"

        # ------------------------------------------------------------------
        # Storage
        # ------------------------------------------------------------------
        print_group "Collecting storage information..."

        awk '/=== ISCSI_SESSIONS ===/,/=== ISCSI_INITIATOR ===/' "$tmp_out" | sed '1d;$d' \
            > "$BASE_OUTPUT_DIR/node-diagnostics/storage/${node}_iscsi-sessions.txt"

        awk '/=== ISCSI_INITIATOR ===/,/=== MULTIPATH_LL ===/' "$tmp_out" | sed '1d;$d' \
            > "$BASE_OUTPUT_DIR/node-diagnostics/storage/${node}_iscsi-initiatorname.txt"

        awk '/=== MULTIPATH_LL ===/,/=== MULTIPATH_CONF ===/' "$tmp_out" | sed '1d;$d' \
            > "$BASE_OUTPUT_DIR/node-diagnostics/storage/${node}_multipath-ll.txt"

        awk '/=== MULTIPATH_CONF ===/,/=== LSBLK ===/' "$tmp_out" | sed '1d;$d' \
            > "$BASE_OUTPUT_DIR/node-diagnostics/storage/${node}_multipath.conf"

        awk '/=== LSBLK ===/,/=== MOUNTS ===/' "$tmp_out" | sed '1d;$d' \
            > "$BASE_OUTPUT_DIR/node-diagnostics/storage/${node}_lsblk.txt"

        awk '/=== MOUNTS ===/,/=== IP_ADDR ===/' "$tmp_out" | sed '1d;$d' \
            > "$BASE_OUTPUT_DIR/node-diagnostics/storage/${node}_mounts.txt"

        # ------------------------------------------------------------------
        # Network
        # ------------------------------------------------------------------
        print_group "Collecting network information..."

        awk '/=== IP_ADDR ===/,/=== IP_ROUTE ===/' "$tmp_out" | sed '1d;$d' \
            > "$BASE_OUTPUT_DIR/node-diagnostics/network/${node}_ip-addr.txt"

        awk '/=== IP_ROUTE ===/,/=== KUBELET_JOURNAL ===/' "$tmp_out" | sed '1d;$d' \
            > "$BASE_OUTPUT_DIR/node-diagnostics/network/${node}_ip-route.txt"

        # ------------------------------------------------------------------
        # Kubelet journal
        # ------------------------------------------------------------------
        print_group "Collecting Kubelet Journal information..."

        awk '/=== KUBELET_JOURNAL ===/,/=== END ===/' "$tmp_out" | sed '1d;$d' \
            > "$BASE_OUTPUT_DIR/node-diagnostics/kubelet/${node}_kubelet-journal.txt"

        echo ""
    done
}

# ==============================================================================
# PHASE 8: STORAGE SYSTEM DIAGNOSTICS (SVC / FlashSystem)
# ==============================================================================
setup_ssh_authentication() {
    local password="$1"

    local ssh_dir
    ssh_dir="$(mktemp -d)"
    TEMP_DIRS+=("$ssh_dir")

    local askpass_script="$ssh_dir/askpass.sh"

    cat > "$askpass_script" << 'EOF'
#!/bin/sh
echo "$SSH_PASSWORD"
EOF

    chmod 700 "$askpass_script"
    TEMP_FILES+=("$askpass_script")

    export SSH_ASKPASS="$askpass_script"
    export SSH_ASKPASS_REQUIRE=force
    export SSH_PASSWORD="$password"
    export DISPLAY=:0

    log_success "SSH authentication configured"
}

collect_storage_diagnostics() {

    [[ "$COLLECT_STORAGE_SYSTEM" != true ]] && return

    print_section "Collecting Storage System Diagnostics"

    local mgmt_address username password

    mgmt_address=$($KUBE_CMD get secret "$STORAGE_SECRET_NAME" \
        -n "$STORAGE_SECRET_NAMESPACE" \
        -o jsonpath='{.data.management_address}' 2>/dev/null | base64 -d)

    username=$($KUBE_CMD get secret "$STORAGE_SECRET_NAME" \
        -n "$STORAGE_SECRET_NAMESPACE" \
        -o jsonpath='{.data.username}' 2>/dev/null | base64 -d)

    password=$($KUBE_CMD get secret "$STORAGE_SECRET_NAME" \
        -n "$STORAGE_SECRET_NAMESPACE" \
        -o jsonpath='{.data.password}' 2>/dev/null | base64 -d)

    if [[ -z "$mgmt_address" || -z "$username" || -z "$password" ]]; then
        log_error "Storage credentials incomplete"
        return 1
    fi

    log_info "Processing storage system: $mgmt_address"

    print_group "Authenticating to storage system..."
    setup_ssh_authentication "$password" && print_ok "Credentials retrieved"

    local ssh_opts=(
        -o StrictHostKeyChecking=no
        -o UserKnownHostsFile=/dev/null
        -o ConnectTimeout="${STORAGE_CONNECTION_TIMEOUT}"
        -o BatchMode=no
    )

    if ! setsid ssh "${ssh_opts[@]}" "${username}@${mgmt_address}" < /dev/null \
        echo OK >/dev/null 2>&1; then
        print_warn "SSH authentication failed"
        return 1
    fi

    print_ok "SSH authentication successful"

    local storage_output
    storage_output="$(mktemp)"
    TEMP_FILES+=("$storage_output")

    print_group "Collecting storage diagnostics..."

    if ! setsid ssh "${ssh_opts[@]}" "${username}@${mgmt_address}" < /dev/null \
        > "$storage_output" 2>&1 << 'REMOTE_SCRIPT'
echo "=== EVENTLOG ==="
lseventlog

echo "=== SYSTEM ==="
lssystem

echo "=== HOSTS ==="
lshost

echo "=== FABRIC ==="
lsfabric

echo "=== PORTIP ==="
lsportip

echo "=== VDISK ==="
lsvdisk

echo "=== HOST_VDISK_MAP ==="
lshostvdiskmap

echo "=== AUDITLOG ==="
catauditlog

echo "=== END ==="
REMOTE_SCRIPT
    then
        print_warn "SSH session failed"
        return 1
    fi

    mkdir -p "$BASE_OUTPUT_DIR/storage-system"

    awk '/=== EVENTLOG ===/,/=== SYSTEM ===/' "$storage_output" | sed '1d;$d' \
        > "$BASE_OUTPUT_DIR/storage-system/event-log.txt" \
        && print_ok "Event log" || print_warn "Event log"

    awk '/=== SYSTEM ===/,/=== HOSTS ===/' "$storage_output" | sed '1d;$d' \
        > "$BASE_OUTPUT_DIR/storage-system/system-info.txt" \
        && print_ok "System information" || print_warn "System information"

    awk '/=== HOSTS ===/,/=== FABRIC ===/' "$storage_output" | sed '1d;$d' \
        > "$BASE_OUTPUT_DIR/storage-system/hosts.txt" \
        && print_ok "Host definitions" || print_warn "Host definitions"

    awk '/=== FABRIC ===/,/=== PORTIP ===/' "$storage_output" | sed '1d;$d' \
        > "$BASE_OUTPUT_DIR/storage-system/fc-fabric.txt" \
        && print_ok "FC fabric information" || print_warn "FC fabric information"

    awk '/=== PORTIP ===/,/=== VDISK ===/' "$storage_output" | sed '1d;$d' \
        > "$BASE_OUTPUT_DIR/storage-system/iscsi-ports.txt" \
        && print_ok "iSCSI port configuration" || print_warn "iSCSI port configuration"

    awk '/=== VDISK ===/,/=== HOST_VDISK_MAP ===/' "$storage_output" | sed '1d;$d' \
        > "$BASE_OUTPUT_DIR/storage-system/volumes.txt" \
        && print_ok "Volume list" || print_warn "Volume list"

    awk '/=== HOST_VDISK_MAP ===/,/=== AUDITLOG ===/' "$storage_output" | sed '1d;$d' \
        > "$BASE_OUTPUT_DIR/storage-system/host-vdisk-map.txt" \
        && print_ok "Host–VDisk mapping" || print_warn "Host–VDisk mapping"

    awk '/=== AUDITLOG ===/,/=== END ===/' "$storage_output" | sed '1d;$d' \
        > "$BASE_OUTPUT_DIR/storage-system/audit-log.txt" \
        && print_ok "Audit log" || print_warn "Audit log"

    echo ""
}


# ==============================================================================
# PHASE 9: WORKLOAD
# ==============================================================================
collect_workload_context() {
    [[ "$COLLECT_WORKLOAD" != true ]] && return
    [[ -z "$WORKLOAD_POD" && -z "$WORKLOAD_PVC" ]] && return

    print_section "Collecting Workload Context"

    mkdir -p \
        "$WORKLOAD_DIR/pods" \
        "$WORKLOAD_DIR/pvcs" \
        "$WORKLOAD_DIR/pvs" \
        "$WORKLOAD_DIR/events" \
        "$WORKLOAD_DIR/csi-node-logs"

    local pods=()
    local pvcs=()
    local nodes=()

    if [[ -n "$WORKLOAD_POD" ]]; then
        cat > "$WORKLOAD_DIR/target.txt" << EOF
workload-type: pod
workload-name: $WORKLOAD_POD
namespace: $TARGET_NAMESPACE
EOF
    elif [[ -n "$WORKLOAD_PVC" ]]; then
        cat > "$WORKLOAD_DIR/target.txt" << EOF
workload-type: pvc
workload-name: $WORKLOAD_PVC
namespace: $TARGET_NAMESPACE
EOF
    else
        return
    fi

    # ------------------------------------------------------------
    # POD TARGET
    # ------------------------------------------------------------
    if [[ -n "$WORKLOAD_POD" ]]; then
        pods+=("$WORKLOAD_POD")

        while IFS= read -r pvc; do
            [[ -n "$pvc" ]] && pvcs+=("$pvc")
        done < <(get_pod_pvcs "$WORKLOAD_POD")

        if [[ ${#pvcs[@]} -eq 0 ]]; then
            log_warning "Pod '$WORKLOAD_POD' has no PVCs attached"
            log_warning "Only pod, node, and CSI context will be collected"
        fi
    fi

    # ------------------------------------------------------------
    # PVC TARGET
    # ------------------------------------------------------------
    if [[ -n "$WORKLOAD_PVC" ]]; then
        pvcs+=("$WORKLOAD_PVC")

        while IFS= read -r pod; do
            [[ -n "$pod" ]] && pods+=("$pod")
        done < <(get_pods_using_pvc "$WORKLOAD_PVC")

        if [[ ${#pods[@]} -eq 0 ]]; then
            log_warning "No pods found using PVC '$WORKLOAD_PVC' in namespace '$TARGET_NAMESPACE'"
            log_warning "Only PVC and PV metadata will be collected"
        fi
    fi

    # ------------------------------------------------------------
    # Deduplicate
    # ------------------------------------------------------------
    pods=($(printf "%s\n" "${pods[@]}" | sort -u))
    pvcs=($(printf "%s\n" "${pvcs[@]}" | sort -u))

    # ------------------------------------------------------------
    # Pods
    # ------------------------------------------------------------
    for pod in "${pods[@]}"; do
        mkdir -p "$WORKLOAD_DIR/pods/$pod"

        $KUBE_CMD get pod "$pod" -n "$TARGET_NAMESPACE" -o yaml \
            > "$WORKLOAD_DIR/pods/$pod/pod.yaml"

        $KUBE_CMD describe pod "$pod" -n "$TARGET_NAMESPACE" \
            > "$WORKLOAD_DIR/pods/$pod/pod-describe.txt"
    done

    # ------------------------------------------------------------
    # PVCs and PVs
    # ------------------------------------------------------------
    for pvc in "${pvcs[@]}"; do
        mkdir -p "$WORKLOAD_DIR/pvcs/$pvc"

        $KUBE_CMD get pvc "$pvc" -n "$TARGET_NAMESPACE" -o yaml \
            > "$WORKLOAD_DIR/pvcs/$pvc/pvc.yaml"

        $KUBE_CMD describe pvc "$pvc" -n "$TARGET_NAMESPACE" \
            > "$WORKLOAD_DIR/pvcs/$pvc/pvc-describe.txt"

        local pv
        pv=$($KUBE_CMD get pvc "$pvc" -n "$TARGET_NAMESPACE" \
            -o jsonpath='{.spec.volumeName}')

        if [[ -n "$pv" ]]; then
            mkdir -p "$WORKLOAD_DIR/pvs/$pv"

            $KUBE_CMD get pv "$pv" -o yaml \
                > "$WORKLOAD_DIR/pvs/$pv/pv.yaml"

            $KUBE_CMD describe pv "$pv" \
                > "$WORKLOAD_DIR/pvs/$pv/pv-describe.txt"
        fi
    done

    # ------------------------------------------------------------
    # Namespace events
    # ------------------------------------------------------------
    $KUBE_CMD get events -n "$TARGET_NAMESPACE" -o json \
        > "$WORKLOAD_DIR/events/events.json"

    filter_events_by_time "$WORKLOAD_DIR/events/events.json"

    if [[ "$(jq '.items | length' "$WORKLOAD_DIR/events/events.json")" -gt 0 ]]; then
        jq '.' "$WORKLOAD_DIR/events/events.json" | \
            $KUBE_CMD apply --dry-run=client -f - -o yaml \
            > "$WORKLOAD_DIR/events/namespace-events.yaml"
    else
        log_warning "No namespace events found for workload time window"
    fi

    rm -f "$WORKLOAD_DIR/events/events.json"

    # ------------------------------------------------------------
    # CSI node plugin logs (per node)
    # ------------------------------------------------------------
    for pod in "${pods[@]}"; do
        local node
        node="$(get_pod_node "$pod")"
        [[ -n "$node" ]] && nodes+=("$node")
    done

    nodes=($(printf "%s\n" "${nodes[@]}" | sort -u))

    if [[ ${#nodes[@]} -eq 0 ]]; then
        log_info "Skipping CSI node logs (no workload nodes found)"
        return
    fi

    for node in "${nodes[@]}"; do
        mkdir -p "$WORKLOAD_DIR/csi-node-logs/$node"

        $KUBE_CMD get pods --all-namespaces \
            -l product=ibm-block-csi-driver \
            -l app.kubernetes.io/component=csi-node \
            -o json | \
        jq -r --arg node "$node" '
            .items[] |
            select(.spec.nodeName == $node) |
            [.metadata.name, .metadata.namespace] | @tsv
        ' | while read -r csi_pod ns; do
            collect_pod_logs \
                "$csi_pod" "$ns" \
                "$WORKLOAD_DIR/csi-node-logs/$node" \
                "$(build_time_flags)"
        done
    done
}

# ==============================================================================
# PHASE 10: SUMMARY
# ==============================================================================
generate_summary() {
    print_section "Generating Collection Summary"

    local total_files
    total_files=$(find "$BASE_OUTPUT_DIR" -type f | wc -l)

    local time_filter_info
    if [[ -n "$SINCE_DURATION" ]]; then
        time_filter_info="Logs from last $SINCE_DURATION"
    elif [[ -n "$USER_START_TIME" ]]; then
        if [[ "$USER_END_TIME" == "$(date -u +%Y-%m-%dT%H:%M)" ]]; then
            time_filter_info="Logs from $USER_START_TIME until now (UTC)"
        else
            time_filter_info="Logs from $USER_START_TIME until $USER_END_TIME (UTC)"
        fi
    else
        time_filter_info="No time filtering (all logs)"
    fi

    local cluster_type
    cluster_type=$([[ "$IS_OPENSHIFT" == true ]] && echo "OpenShift" || echo "Kubernetes")

    cat > "$BASE_OUTPUT_DIR/COLLECTION_SUMMARY.txt" << EOF
============================================================
IBM Block CSI Driver – Diagnostics Collection Summary
============================================================

Collected At (UTC) : $(date -u +"%Y-%m-%d %H:%M:%S")
Script Version     : $VERSION
Cluster Type       : $cluster_type
Cluster CLI Used   : $KUBE_CMD
Target Namespace   : ${TARGET_NAMESPACE:-All namespaces}

-----------------------------
Time Filtering (Logs & Events)
-----------------------------
Applied            : $time_filter_info
Log Size Limit     : ${LOG_LIMIT_BYTES:-Default}

--------------------
Components Collected
--------------------
Resources           : $COLLECT_RESOURCES
Logs                : $COLLECT_LOGS
Events              : $COLLECT_EVENTS
Node Diagnostics    : $COLLECT_NODE_DIAGNOSTICS
Storage System      : $COLLECT_STORAGE_SYSTEM
Workload            : $COLLECT_WORKLOAD

Total Files Collected: $total_files

----------------
Output Directory : $(realpath "$BASE_OUTPUT_DIR")
----------------

EOF
    log_success "Collection summary generated"
}


# ==============================================================================
# MAIN EXECUTION
# ==============================================================================
main() {
    echo ""
    echo -e "${BLUE}============================================================${NC}"
    echo -e "${BLUE}IBM Block CSI Driver Diagnostics Collection Script${NC}"
    echo -e "${BLUE}Version: ${VERSION}${NC}"
    echo -e "${BLUE}============================================================${NC}"
    echo ""

    parse_arguments "$@"
    check_prerequisites
    
    print_section "Collection Configuration"
    
    # Namespace configuration
    if [[ -n "$TARGET_NAMESPACE" ]]; then
        log_info "Target Namespace: ${GREEN}$TARGET_NAMESPACE${NC}"
    else
        log_info "Target Namespace: ${GREEN}--all-namespaces${NC}"
    fi
    
    # Storage configuration check
    if [[ "$COLLECT_STORAGE_SYSTEM" == true ]]; then
        if [[ -z "$STORAGE_SECRET_NAME" || -z "$STORAGE_SECRET_NAMESPACE" ]]; then
            echo ""
            COLLECT_STORAGE_SYSTEM=false
            log_warning "Storage system diagnostics skipped (Reason: missing --storage-secret & --storage-secret-namespace)"
        fi
    fi
    
    # Workload configuration check
    if [[ "$COLLECT_WORKLOAD" == true ]]; then
        if [[ -z "$WORKLOAD_POD" && -z "$WORKLOAD_PVC" ]]; then
            echo ""
            log_warning "Workload diagnostics skipped (missing --workload-pod or --workload-pvc)"
            COLLECT_WORKLOAD=false
        fi
    fi

    # Components configuration
    echo ""
    log_info "Components to collect:"
    [[ "$COLLECT_RESOURCES" == true ]] && echo -e "  ${GREEN}✓${NC} Resources" || echo -e "  ${YELLOW}✗${NC} Resources (skipped)"
    [[ "$COLLECT_LOGS" == true ]] && echo -e "  ${GREEN}✓${NC} Logs" || echo -e "  ${YELLOW}✗${NC} Logs (skipped)"
    [[ "$COLLECT_EVENTS" == true ]] && echo -e "  ${GREEN}✓${NC} Events" || echo -e "  ${YELLOW}✗${NC} Events (skipped)"
    [[ "$COLLECT_NODE_DIAGNOSTICS" == true ]] && echo -e "  ${GREEN}✓${NC} Node Diagnostics" || echo -e "  ${YELLOW}✗${NC} Node Diagnostics (skipped)"
    [[ "$COLLECT_STORAGE_SYSTEM" == true ]] && echo -e "  ${GREEN}✓${NC} Storage System" || echo -e "  ${YELLOW}✗${NC} Storage System (skipped)"
    [[ "$COLLECT_WORKLOAD" == true ]] && echo -e "  ${GREEN}✓${NC} Workload" || echo -e "  ${YELLOW}✗${NC} Workload (skipped)"

    # Storage configuration
    if [[ "$COLLECT_STORAGE_SYSTEM" == true ]]; then
        echo ""
        log_info "Storage Secret: ${GREEN}$STORAGE_SECRET_NAME${NC}"
        log_info "Storage Secret Namespace: ${GREEN}$STORAGE_SECRET_NAMESPACE${NC}"
    fi

    # Workload targeting configuration
    if [[ "$COLLECT_WORKLOAD" == true ]]; then
        echo ""
        log_info "Workload Targeting:"

        [[ -n "$WORKLOAD_POD" ]] && \
            echo -e "  ${GREEN}Pod${NC}: $WORKLOAD_POD"

        [[ -n "$WORKLOAD_PVC" ]] && \
            echo -e "  ${GREEN}PVC${NC}: $WORKLOAD_PVC"

        echo -e "  ${GREEN}Namespace${NC}: $TARGET_NAMESPACE"
    fi
    echo ""

    # Time filtering configuration (LOGS & EVENTS ONLY)
    if [[ -n "$SINCE_DURATION" ]]; then
        log_info "Time Filter (Logs & Events): ${GREEN}Last $SINCE_DURATION${NC}"
    elif [[ -n "$USER_START_TIME" ]]; then
        if [[ -z "$USER_END_TIME" ]]; then
            log_info "Time Filter (Logs & Events): ${GREEN}From $USER_START_TIME until now (UTC)${NC}"
        else
            log_info "Time Filter (Logs & Events): ${GREEN}From $USER_START_TIME until $USER_END_TIME (UTC)${NC}"
        fi
    else
        log_info "Time Filter (Logs & Events): ${GREEN}None (all logs & events collected)${NC}"
    fi
    echo ""
    
    validate_arguments
    setup_environment

    # Execute collection phases
    collect_all_resources
    collect_workloads
    collect_events
    collect_logs
    collect_node_diagnostics
    collect_storage_diagnostics
    
    generate_summary
    
    print_section "Collection Complete"
    log_info "Output directory: $(realpath "$BASE_OUTPUT_DIR")"
    echo ""
    
}

main "$@"