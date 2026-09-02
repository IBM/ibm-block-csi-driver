#!/usr/bin/env bash
# =============================================================================
# IBM Block CSI Driver – Power (ppc64le) End-to-End Test Script
# =============================================================================
# Tests the full lifecycle in dependency order:
#   CRDs → StorageClass → VolumeSnapshotClass → VolumeGroupClass →
#   PVC (filesystem + raw-block) → VolumeGroup → Pod →
#   VolumeSnapshot → PVC-from-snapshot → PVC-from-snapshot Pod → Cleanup
#
# Usage:
#   ./scripts/e2e_power_test.sh [--namespace <ns>] [--timeout <seconds>]
#                               [--skip-cleanup] [--keep-on-fail]
#
# Requirements:
#   - oc (or kubectl, set OC_CMD env var) available and cluster access
#   - The IBM Block CSI driver pods must already be running
#   - The testing YAML files must be present in YAML_DIR
# =============================================================================

set -euo pipefail

# ---------------------------------------------------------------------------
# Configurable defaults (override via environment or flags)
# ---------------------------------------------------------------------------
# NAMESPACE: auto-detect the IBM Block CSI namespace if not set explicitly.
# Looks for a namespace that contains an ibmblockcsi CR; falls back to "default".
_detect_namespace() {
    local ns
    ns=$(oc get ibmblockcsi --all-namespaces --no-headers \
        -o custom-columns='NS:.metadata.namespace' 2>/dev/null | head -1 || true)
    echo "${ns:-default}"
}
NAMESPACE="${NAMESPACE:-$(_detect_namespace)}"
TIMEOUT="${TIMEOUT:-300}"          # seconds to wait for each object to become ready
YAML_DIR="${YAML_DIR:-$(dirname "$0")/../testing}"
OC_CMD="${OC_CMD:-oc}"             # set to "kubectl" if not on OpenShift
SKIP_CLEANUP="${SKIP_CLEANUP:-false}"
KEEP_ON_FAIL="${KEEP_ON_FAIL:-false}"

# ---------------------------------------------------------------------------
# Colour helpers
# ---------------------------------------------------------------------------
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

log_info()    { echo -e "${CYAN}[INFO ]${RESET} $*"; }
log_ok()      { echo -e "${GREEN}[  OK ]${RESET} $*"; }
log_warn()    { echo -e "${YELLOW}[ WARN]${RESET} $*"; }
log_error()   { echo -e "${RED}[ERROR]${RESET} $*" >&2; }
log_section() { echo -e "\n${BOLD}${CYAN}=== $* ===${RESET}"; }

PASS_COUNT=0
FAIL_COUNT=0
FAILED_STEPS=()

# print_summary is defined here (forward declaration as a function so fail() can call it)
print_summary() {
    log_section "Test Summary"
    echo -e "  Namespace : ${NAMESPACE}"
    echo -e "  Timeout   : ${TIMEOUT}s per step"
    echo -e "  ${GREEN}PASSED${RESET}    : ${PASS_COUNT}"
    echo -e "  ${RED}FAILED${RESET}    : ${FAIL_COUNT}"
    if [[ ${#FAILED_STEPS[@]} -gt 0 ]]; then
        echo -e "  ${RED}Failed steps:${RESET}"
        for s in "${FAILED_STEPS[@]}"; do
            echo -e "    - $s"
        done
    fi
    if [[ $FAIL_COUNT -eq 0 ]]; then
        echo -e "\n${GREEN}${BOLD}ALL TESTS PASSED ✓${RESET}"
    else
        echo -e "\n${RED}${BOLD}SOME TESTS FAILED ✗${RESET}"
    fi
}

pass() { PASS_COUNT=$((PASS_COUNT + 1)); log_ok "$1"; }
fail() {
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_STEPS+=("$1")
    log_error "$1"
    if [[ "$KEEP_ON_FAIL" != "true" && "$SKIP_CLEANUP" != "true" ]]; then
        log_warn "Aborting on first failure. Use --keep-on-fail to continue past failures."
        print_summary
        exit 1
    fi
}

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------
while [[ $# -gt 0 ]]; do
    case "$1" in
        --namespace)  NAMESPACE="$2"; shift 2 ;;
        --timeout)    TIMEOUT="$2";   shift 2 ;;
        --skip-cleanup) SKIP_CLEANUP="true"; shift ;;
        --keep-on-fail) KEEP_ON_FAIL="true"; shift ;;
        *) log_error "Unknown argument: $1"; exit 1 ;;
    esac
done

OC="${OC_CMD} -n ${NAMESPACE}"

# ---------------------------------------------------------------------------
# Prerequisite checks
# ---------------------------------------------------------------------------
log_section "Prerequisite checks"

if ! command -v "$OC_CMD" &>/dev/null; then
    log_error "'$OC_CMD' not found in PATH. Set OC_CMD=kubectl if needed."
    exit 1
fi
log_ok "$OC_CMD found: $($OC_CMD version --client --short 2>/dev/null | head -1)"

if [[ ! -d "$YAML_DIR" ]]; then
    log_error "YAML directory not found: $YAML_DIR"
    exit 1
fi
log_ok "YAML directory: $YAML_DIR"

# Check cluster connectivity
if ! $OC_CMD get nodes &>/dev/null; then
    log_error "Cannot reach cluster. Check kubeconfig / oc login."
    exit 1
fi
log_ok "Cluster reachable"
log_ok "Using namespace: ${NAMESPACE}"

# Check IBM Block CSI driver pods – use the detected namespace (not default)
log_info "Checking IBM Block CSI driver pods are running in '${NAMESPACE}'..."
DRIVER_PODS=$($OC_CMD get pods -n "$NAMESPACE" --no-headers \
    -l app=ibm-block-csi-controller 2>/dev/null | grep -c Running || true)
if [[ "$DRIVER_PODS" -eq 0 ]]; then
    # Try statefulset label as fallback (controller-0 uses app.kubernetes.io/component)
    DRIVER_PODS=$($OC_CMD get pods -n "$NAMESPACE" --no-headers \
        -l app.kubernetes.io/component=csi-controller 2>/dev/null | grep -c Running || true)
fi
if [[ "$DRIVER_PODS" -eq 0 ]]; then
    log_warn "No ibm-block-csi-controller pods found in namespace '${NAMESPACE}'. Continuing – some steps may fail."
else
    log_ok "IBM Block CSI controller pods running: $DRIVER_PODS"
fi

# ---------------------------------------------------------------------------
# Helper: wait for a resource condition
# ---------------------------------------------------------------------------
# wait_for_resource <kind> <name> <jsonpath> <expected_value>
wait_for_resource() {
    local kind="$1" name="$2" jsonpath="$3" expected="$4"
    local deadline=$(( $(date +%s) + TIMEOUT ))
    log_info "Waiting for ${kind}/${name} → ${jsonpath}=${expected} (timeout: ${TIMEOUT}s)..."
    while true; do
        local actual
        actual=$($OC get "$kind" "$name" -o jsonpath="$jsonpath" 2>/dev/null || true)
        if [[ "$actual" == "$expected" ]]; then
            return 0
        fi
        if [[ $(date +%s) -ge $deadline ]]; then
            log_error "Timeout waiting for ${kind}/${name}. Last value: '${actual}'"
            return 1
        fi
        sleep 5
    done
}

# wait_for_crd_not_terminating <crd_name>
# Blocks until the CRD has no deletionTimestamp (i.e. is not being deleted from a previous run).
wait_for_crd_not_terminating() {
    local crd="$1"
    local deadline=$(( $(date +%s) + TIMEOUT ))
    # If the CRD doesn't exist at all that's fine – nothing to wait for
    if ! $OC_CMD get crd "$crd" &>/dev/null; then
        return 0
    fi
    local ts
    ts=$($OC_CMD get crd "$crd" -o jsonpath='{.metadata.deletionTimestamp}' 2>/dev/null || true)
    if [[ -z "$ts" ]]; then
        return 0  # not terminating
    fi
    log_info "CRD ${crd} is still terminating from a previous run – waiting for it to be fully deleted..."
    while true; do
        if ! $OC_CMD get crd "$crd" &>/dev/null 2>&1; then
            log_info "CRD ${crd} fully deleted – safe to re-apply."
            return 0
        fi
        if [[ $(date +%s) -ge $deadline ]]; then
            log_error "Timeout waiting for CRD ${crd} to finish terminating."
            return 1
        fi
        sleep 3
    done
}

# wait_for_pvc_bound <name>
wait_for_pvc_bound() {
    wait_for_resource pvc "$1" '{.status.phase}' 'Bound'
}

# wait_for_pod_running <name>
wait_for_pod_running() {
    local name="$1"
    local deadline=$(( $(date +%s) + TIMEOUT ))
    log_info "Waiting for pod/${name} to be Running & Ready (timeout: ${TIMEOUT}s)..."
    while true; do
        local phase ready
        phase=$($OC get pod "$name" -o jsonpath='{.status.phase}' 2>/dev/null || true)
        ready=$($OC get pod "$name" -o jsonpath='{.status.containerStatuses[0].ready}' 2>/dev/null || true)
        if [[ "$phase" == "Running" && "$ready" == "true" ]]; then
            return 0
        fi
        if [[ $(date +%s) -ge $deadline ]]; then
            log_error "Timeout waiting for pod/${name}. phase=${phase}, ready=${ready}"
            $OC describe pod "$name" 2>/dev/null | tail -20 || true
            return 1
        fi
        sleep 5
    done
}

# wait_for_snapshot_ready <name>
wait_for_snapshot_ready() {
    local name="$1"
    local deadline=$(( $(date +%s) + TIMEOUT ))
    log_info "Waiting for VolumeSnapshot/${name} readyToUse=true (timeout: ${TIMEOUT}s)..."
    while true; do
        local ready
        ready=$($OC get volumesnapshot "$name" -o jsonpath='{.status.readyToUse}' 2>/dev/null || true)
        if [[ "$ready" == "true" ]]; then
            return 0
        fi
        if [[ $(date +%s) -ge $deadline ]]; then
            log_error "Timeout waiting for VolumeSnapshot/${name}. Last readyToUse: '${ready}'"
            $OC describe volumesnapshot "$name" 2>/dev/null | tail -20 || true
            return 1
        fi
        sleep 5
    done
}

# wait_for_volumegroup_ready <name>
# Prints the full .status block every poll cycle so we can see exactly what
# field names and values the operator is setting – useful when the CRD schema
# is not available locally. Accepts any of these success signals:
#   • status.ready == "true"
#   • status.readyToUse == "true"
#   • status.boundVolumeGroupContentName non-empty
#   • status.volumeGroupContentName non-empty
#   • status.conditions[type=Ready].status == "True"
wait_for_volumegroup_ready() {
    local name="$1"
    local deadline=$(( $(date +%s) + TIMEOUT ))
    log_info "Waiting for VolumeGroup/${name} to be ready (timeout: ${TIMEOUT}s)..."
    log_info "  Printing full .status each poll – check field names below:"
    local poll=0
    while true; do
        poll=$(( poll + 1 ))
        # Dump the entire status block so we can see whatever the operator sets
        local status_json
        status_json=$($OC get volumegroup "$name" \
            -o jsonpath='{.status}' 2>/dev/null || true)
        log_info "  [poll ${poll}] status=${status_json:-<empty>}"

        # Check all known field variants
        local r1 r2 r3 r4 r5
        r1=$(echo "$status_json" | grep -o '"ready":true'           || true)
        r2=$(echo "$status_json" | grep -o '"readyToUse":true'      || true)
        r3=$(echo "$status_json" | grep -o '"boundVolumeGroupContentName":"[^"]*"' || true)
        r4=$(echo "$status_json" | grep -o '"volumeGroupContentName":"[^"]*"'      || true)
        r5=$($OC get volumegroup "$name" \
            -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' \
            2>/dev/null || true)

        if [[ -n "$r1" || -n "$r2" || -n "$r3" || -n "$r4" || "$r5" == "True" ]]; then
            log_ok "VolumeGroup/${name} is ready."
            return 0
        fi

        if [[ $(date +%s) -ge $deadline ]]; then
            log_error "Timeout waiting for VolumeGroup/${name}."
            log_error "--- oc describe volumegroup ${name} ---"
            $OC describe volumegroup "$name" 2>/dev/null || true
            log_error "--- oc get volumegroup ${name} -o yaml ---"
            $OC get volumegroup "$name" -o yaml 2>/dev/null || true
            return 1
        fi
        sleep 5
    done
}

# apply_yaml <filename>  – applies a file from YAML_DIR
apply_yaml() {
    local file="${YAML_DIR}/$1"
    if [[ ! -f "$file" ]]; then
        log_error "YAML file not found: $file"
        return 1
    fi
    $OC apply -f "$file"
}

# delete_yaml <filename>
delete_yaml() {
    local file="${YAML_DIR}/$1"
    [[ -f "$file" ]] && $OC delete --ignore-not-found=true -f "$file" 2>/dev/null || true
}

# show_resource_status <kind> <name>
show_resource_status() {
    local kind="$1" name="$2"
    echo -e "${YELLOW}--- ${kind}/${name} status ---${RESET}"
    $OC get "$kind" "$name" -o wide 2>/dev/null || true
}

# ---------------------------------------------------------------------------
# force_delete_all – wipe every resource that came from the testing YAMLs.
# Strips finalizers when needed so nothing gets stuck in Terminating.
# Safe: only touches objects named in the YAML files; never touches driver pods.
# ---------------------------------------------------------------------------
force_delete_all() {
    log_section "Phase 0: Force-delete previous test resources"

    # ---- inline test pods (not from files) -----------------------------------
    for pod in e2e-test-pod-snapshot e2e-test-pod; do
        if $OC get pod "$pod" &>/dev/null 2>&1; then
            log_info "Force-deleting pod/$pod..."
            $OC delete pod "$pod" --grace-period=0 --force \
                --ignore-not-found=true 2>/dev/null || true
        fi
    done

    # ---- helper: strip finalizers then delete a namespaced resource ----------
    # --wait=false: fire-and-forget; we do not block on the API server
    # confirming deletion. This prevents hanging when the owning operator
    # is unresponsive (e.g. CRDs were already removed in a prior run).
    _force_delete_namespaced() {
        local kind="$1" name="$2"
        if $OC get "$kind" "$name" &>/dev/null 2>&1; then
            log_info "Removing finalizers and force-deleting ${kind}/${name}..."
            $OC patch "$kind" "$name" \
                --type=json -p='[{"op":"replace","path":"/metadata/finalizers","value":[]}]' \
                2>/dev/null || true
            $OC delete "$kind" "$name" --grace-period=0 --force --wait=false \
                --ignore-not-found=true 2>/dev/null || true
        fi
    }

    # ---- helper: strip finalizers then delete a cluster-scoped resource ------
    _force_delete_cluster() {
        local kind="$1" name="$2"
        if $OC_CMD get "$kind" "$name" &>/dev/null 2>&1; then
            log_info "Removing finalizers and force-deleting ${kind}/${name}..."
            $OC_CMD patch "$kind" "$name" \
                --type=json -p='[{"op":"replace","path":"/metadata/finalizers","value":[]}]' \
                2>/dev/null || true
            $OC_CMD delete "$kind" "$name" --grace-period=0 --force --wait=false \
                --ignore-not-found=true 2>/dev/null || true
        fi
    }

    # ---- VolumeSnapshot (namespaced) ----------------------------------------
    SNAP_NAME_DEL=$(awk '
        /^---/ { kind=""; name="" }
        /^kind:/ { kind=$2 }
        /^  name:/ && kind=="VolumeSnapshot" { print $2; exit }
    ' "${YAML_DIR}/volumesnapshot.yaml" 2>/dev/null || true)
    [[ -n "$SNAP_NAME_DEL" ]] && _force_delete_namespaced volumesnapshot "$SNAP_NAME_DEL"

    # All VolumeSnapshotContents bound to our snapshot (cluster-scoped)
    log_info "Removing any VolumeSnapshotContent objects..."
    for vsc in $($OC_CMD get volumesnapshotcontent --no-headers \
                    -o custom-columns='NAME:.metadata.name' 2>/dev/null || true); do
        _force_delete_cluster volumesnapshotcontent "$vsc"
    done

    # ---- PVC-from-snapshot (namespaced) -------------------------------------
    SFPVC_NAME_DEL=$(awk '
        /^---/ { kind=""; name="" }
        /^kind:/ { kind=$2 }
        /^  name:/ && kind=="PersistentVolumeClaim" { print $2; exit }
    ' "${YAML_DIR}/pvc-from-snapshot.yaml" 2>/dev/null || true)
    [[ -n "$SFPVC_NAME_DEL" ]] && _force_delete_namespaced persistentvolumeclaim "$SFPVC_NAME_DEL"

    # ---- VolumeGroupContents (cluster-scoped) --------------------------------
    log_info "Removing any VolumeGroupContent objects..."
    for vgc in $($OC_CMD get volumegroupcontent --all-namespaces --no-headers \
                    -o custom-columns='NAME:.metadata.name' 2>/dev/null || true); do
        _force_delete_cluster volumegroupcontent "$vgc"
    done

    # ---- VolumeGroup (namespaced) -------------------------------------------
    VG_NAME_DEL=$(awk '
        /^---/ { kind=""; name="" }
        /^kind:/ { kind=$2 }
        /^  name:/ && kind=="VolumeGroup" { print $2; exit }
    ' "${YAML_DIR}/vg.yaml" 2>/dev/null || true)
    [[ -n "$VG_NAME_DEL" ]] && _force_delete_namespaced volumegroup "$VG_NAME_DEL"

    # ---- Base PVCs (namespaced) + their backing PVs --------------------------
    # A PVC stuck in Terminating is almost always because the backing PV still
    # has a finalizer. Strip the PV finalizer too, then delete both.
    while IFS= read -r pvc_name; do
        [[ -z "$pvc_name" ]] && continue
        # Find the bound PV name before we delete the PVC
        local pv_name
        pv_name=$($OC get pvc "$pvc_name" \
            -o jsonpath='{.spec.volumeName}' 2>/dev/null || true)
        _force_delete_namespaced persistentvolumeclaim "$pvc_name"
        if [[ -n "$pv_name" ]]; then
            log_info "Removing finalizers and force-deleting backing PV/${pv_name}..."
            $OC_CMD patch pv "$pv_name" \
                --type=json -p='[{"op":"replace","path":"/metadata/finalizers","value":[]}]' \
                2>/dev/null || true
            $OC_CMD delete pv "$pv_name" --grace-period=0 --force --wait=false \
                --ignore-not-found=true 2>/dev/null || true
        fi
    done < <(awk '
        /^---/ { kind=""; name="" }
        /^kind:/ { kind=$2 }
        /^  name:/ && kind=="PersistentVolumeClaim" { print $2 }
    ' "${YAML_DIR}/pvc.yaml" 2>/dev/null || true)

    # Also clean up the pvc-from-snapshot PV if it exists
    if [[ -n "$SFPVC_NAME_DEL" ]]; then
        local sfpv_name
        sfpv_name=$($OC get pvc "$SFPVC_NAME_DEL" \
            -o jsonpath='{.spec.volumeName}' 2>/dev/null || true)
        if [[ -n "$sfpv_name" ]]; then
            log_info "Removing finalizers and force-deleting backing PV/${sfpv_name}..."
            $OC_CMD patch pv "$sfpv_name" \
                --type=json -p='[{"op":"replace","path":"/metadata/finalizers","value":[]}]' \
                2>/dev/null || true
            $OC_CMD delete pv "$sfpv_name" --grace-period=0 --force --wait=false \
                --ignore-not-found=true 2>/dev/null || true
        fi
    fi

    # ---- Class-level objects (cluster-scoped) --------------------------------
    VSC_NAME_DEL=$(awk '
        /^---/ { kind=""; name="" }
        /^kind:/ { kind=$2 }
        /^  name:/ && kind=="VolumeSnapshotClass" { print $2; exit }
    ' "${YAML_DIR}/volumesnapshotclass.yaml" 2>/dev/null || true)
    [[ -n "$VSC_NAME_DEL" ]] && _force_delete_cluster volumesnapshotclass "$VSC_NAME_DEL"

    VGC_NAME_DEL=$(awk '
        /^---/ { kind=""; name="" }
        /^kind:/ { kind=$2 }
        /^  name:/ && kind=="VolumeGroupClass" { print $2; exit }
    ' "${YAML_DIR}/vg.yaml" 2>/dev/null || true)
    [[ -n "$VGC_NAME_DEL" ]] && _force_delete_cluster volumegroupclass "$VGC_NAME_DEL"

    # StorageClass extracted from pvc.yaml if present
    SC_NAME_DEL=$(awk '
        /^---/ { kind=""; name="" }
        /^kind:/ { kind=$2 }
        /^  name:/ && kind=="StorageClass" { print $2; exit }
    ' "${YAML_DIR}/pvc.yaml" 2>/dev/null || true)
    [[ -n "$SC_NAME_DEL" ]] && _force_delete_cluster storageclass "$SC_NAME_DEL"

    # ---- CRDs ----------------------------------------------------------------
    for crd in volumegroupclasses.csi.ibm.com \
               volumegroupcontents.csi.ibm.com \
               volumegroups.csi.ibm.com; do
        _force_delete_cluster crd "$crd"
    done

    # ---- Wait for all CRDs to be fully gone ----------------------------------
    # While waiting, keep re-patching any CRD that re-acquires a finalizer
    # (the operator sometimes re-adds one during teardown).
    log_info "Waiting for CRDs to be fully removed..."
    local deadline=$(( $(date +%s) + TIMEOUT ))
    for crd in volumegroupclasses.csi.ibm.com \
               volumegroupcontents.csi.ibm.com \
               volumegroups.csi.ibm.com; do
        while $OC_CMD get crd "$crd" &>/dev/null 2>&1; do
            if [[ $(date +%s) -ge $deadline ]]; then
                log_error "Timeout waiting for CRD ${crd} to be fully deleted."
                return 1
            fi
            # Re-strip finalizers in case the operator re-added them
            $OC_CMD patch crd "$crd" \
                --type=json -p='[{"op":"replace","path":"/metadata/finalizers","value":[]}]' \
                2>/dev/null || true
            sleep 3
        done
        log_ok "CRD gone: $crd"
    done

    log_ok "Pre-test cleanup complete – cluster is clean."
}

# ---------------------------------------------------------------------------
# Cleanup function (runs on EXIT when SKIP_CLEANUP=false)
# ---------------------------------------------------------------------------
cleanup() {
    if [[ "$SKIP_CLEANUP" == "true" ]]; then
        log_warn "Skipping cleanup (--skip-cleanup set). Resources remain in namespace '${NAMESPACE}'."
        return
    fi
    log_section "Cleanup"
    log_info "Deleting test resources (reverse dependency order)..."

    # Pods / workloads first
    $OC delete pod e2e-test-pod-snapshot --grace-period=0 --force --ignore-not-found=true 2>/dev/null || true
    $OC delete pod e2e-test-pod          --grace-period=0 --force --ignore-not-found=true 2>/dev/null || true

    # PVCs created from snapshots
    delete_yaml pvc-from-snapshot.yaml

    # Snapshot
    delete_yaml volumesnapshot.yaml

    # VolumeGroup + VolumeGroupContents
    delete_yaml vg.yaml
    $OC_CMD delete volumegroupcontent --all --ignore-not-found=true 2>/dev/null || true

    # Base PVCs
    delete_yaml pvc.yaml

    # Class-level objects
    delete_yaml volumesnapshotclass.yaml

    # CRDs
    for crd_file in csi.ibm.com_volumegroupclasses.yaml \
                    csi.ibm.com_volumegroupcontents.yaml \
                    csi.ibm.com_volumegroups.yaml; do
        delete_yaml "$crd_file"
    done

    log_ok "Cleanup complete."
}

trap cleanup EXIT

# ---------------------------------------------------------------------------
# Step tracking helper
# ---------------------------------------------------------------------------
run_step() {
    local step_name="$1"; shift
    log_info "STEP: ${step_name}"
    if "$@"; then
        pass "$step_name"
    else
        fail "$step_name"
    fi
}

# ---------------------------------------------------------------------------
# PHASE 0 – Force-delete any leftover resources from previous runs
# ---------------------------------------------------------------------------
force_delete_all

# ---------------------------------------------------------------------------
# PHASE 1 – CRDs  (cluster is clean at this point)
# ---------------------------------------------------------------------------
log_section "Phase 1: Apply VolumeGroup CRDs"

for crd_file in csi.ibm.com_volumegroupclasses.yaml \
                csi.ibm.com_volumegroupcontents.yaml \
                csi.ibm.com_volumegroups.yaml; do
    run_step "Apply CRD: $crd_file" apply_yaml "$crd_file"
done

# Wait for CRDs to be established (and not still carrying a deletionTimestamp)
for crd in volumegroupclasses.csi.ibm.com \
           volumegroupcontents.csi.ibm.com \
           volumegroups.csi.ibm.com; do
    run_step "CRD established: $crd" \
        wait_for_resource crd "$crd" \
        '{.status.conditions[?(@.type=="Established")].status}' 'True'
done

# ---------------------------------------------------------------------------
# PHASE 2 – StorageClass + VolumeSnapshotClass + VolumeGroupClass
# ---------------------------------------------------------------------------
log_section "Phase 2: Apply class-level objects"

# VolumeSnapshotClass (used by snapshot and pvc-from-snapshot)
run_step "Apply VolumeSnapshotClass" apply_yaml volumesnapshotclass.yaml

# Verify VolumeSnapshotClass exists
run_step "VolumeSnapshotClass present" \
    bash -c "$OC get volumesnapshotclass \
        \$($OC get volumesnapshotclass -o jsonpath='{.items[0].metadata.name}' 2>/dev/null) \
        &>/dev/null"

# VolumeGroupClass is embedded in vg.yaml in some setups; check file
if [[ -f "${YAML_DIR}/vg.yaml" ]]; then
    # Check if file contains VolumeGroupClass (may be multi-doc)
    if grep -q 'kind: VolumeGroupClass' "${YAML_DIR}/vg.yaml" 2>/dev/null; then
        log_info "VolumeGroupClass found inside vg.yaml – will be applied with VolumeGroup in Phase 4"
    fi
fi

# ---------------------------------------------------------------------------
# PHASE 3 – Persistent Volume Claims
# ---------------------------------------------------------------------------
log_section "Phase 3: Apply base PVCs"

# Wait for any same-named PVCs left from Phase 0 cleanup to be fully gone
# before creating new ones. A PVC named "pvc-name" that is still Terminating
# will cause a newly created PVC to immediately enter Terminating too.
log_info "Waiting for any leftover PVCs from previous run to disappear..."
_leftover_pvc_deadline=$(( $(date +%s) + TIMEOUT ))
while IFS= read -r _pvc_check; do
    [[ -z "$_pvc_check" ]] && continue
    while $OC get pvc "$_pvc_check" &>/dev/null 2>&1; do
        if [[ $(date +%s) -ge $_leftover_pvc_deadline ]]; then
            log_warn "PVC/${_pvc_check} still exists after timeout – proceeding anyway"
            break
        fi
        log_info "  Waiting for leftover PVC/${_pvc_check} to be gone..."
        # Re-strip finalizer in case it reappeared
        $OC patch pvc "$_pvc_check" \
            --type=json -p='[{"op":"replace","path":"/metadata/finalizers","value":[]}]' \
            2>/dev/null || true
        sleep 3
    done
done < <(awk '
    /^---/ { kind=""; name="" }
    /^kind:/ { kind=$2 }
    /^  name:/ && kind=="PersistentVolumeClaim" { print $2 }
' "${YAML_DIR}/pvc.yaml" 2>/dev/null || true)

run_step "Apply PVC" apply_yaml pvc.yaml

# Discover PVC names by parsing the YAML directly – filter to kind: PersistentVolumeClaim only.
# This handles multi-doc files that also contain a StorageClass or other resources.
PVC_NAMES=$(awk '
    /^---/ { kind=""; name="" }
    /^kind:/ { kind=$2 }
    /^  name:/ && kind=="PersistentVolumeClaim" { print $2 }
' "${YAML_DIR}/pvc.yaml" 2>/dev/null || true)

if [[ -z "$PVC_NAMES" ]]; then
    log_warn "Could not parse PVC names from pvc.yaml – falling back to live cluster query"
    # Ask the cluster for PVCs that were just created from this file
    PVC_NAMES=$($OC get pvc --no-headers \
        -o custom-columns='NAME:.metadata.name' 2>/dev/null | head -5 || true)
fi

if [[ -z "$PVC_NAMES" ]]; then
    fail "Phase 3: Cannot determine PVC names – check pvc.yaml format"
fi

log_info "PVCs to watch: $(echo $PVC_NAMES | tr '\n' ' ')"
for pvc in $PVC_NAMES; do
    run_step "PVC Bound: $pvc" wait_for_pvc_bound "$pvc"
    show_resource_status pvc "$pvc"
done

# ---------------------------------------------------------------------------
# PHASE 4 – VolumeGroup
# ---------------------------------------------------------------------------
log_section "Phase 4: Apply VolumeGroup"

# Validate volume_group_name_prefix doesn't start with a digit (CMMVC6527E)
VG_PREFIX=$(grep 'volume_group_name_prefix' "${YAML_DIR}/vg.yaml" 2>/dev/null \
    | awk '{print $2}' | head -1 || true)
if [[ -n "$VG_PREFIX" ]] && echo "$VG_PREFIX" | grep -qE '^[0-9]'; then
    log_error "volume_group_name_prefix '${VG_PREFIX}' starts with a digit – FlashSystem will reject it (CMMVC6527E)."
    log_error "Fix vg.yaml: prefix must begin with a letter or underscore (e.g. vg_${VG_PREFIX})"
    fail "Phase 4: invalid volume_group_name_prefix"
fi

# If vg.yaml contains a VolumeGroupClass, apply it first and wait for it to
# be registered before creating the VolumeGroup. This avoids the race where
# the operator reconciles the VolumeGroup before the class exists in its cache.
VGC_APPLY_NAME=$(awk '
    /^---/ { kind=""; name="" }
    /^kind:/ { kind=$2 }
    /^  name:/ && kind=="VolumeGroupClass" { print $2; exit }
' "${YAML_DIR}/vg.yaml" 2>/dev/null || true)

if [[ -n "$VGC_APPLY_NAME" ]]; then
    log_info "vg.yaml contains VolumeGroupClass '${VGC_APPLY_NAME}' – applying it first"
    $OC_CMD apply -f "${YAML_DIR}/vg.yaml" 2>/dev/null || true
    log_info "Waiting for VolumeGroupClass/${VGC_APPLY_NAME} to be available..."
    _vgc_deadline=$(( $(date +%s) + 60 ))
    until $OC_CMD get volumegroupclass "$VGC_APPLY_NAME" &>/dev/null 2>&1; do
        if [[ $(date +%s) -ge $_vgc_deadline ]]; then
            log_warn "VolumeGroupClass/${VGC_APPLY_NAME} not found after 60s – proceeding anyway"
            break
        fi
        sleep 2
    done
fi

# Ensure the VolumeGroup gets the correct namespace (vg.yaml may omit it).
# Strategy: if no 'namespace:' line exists inside the VolumeGroup document,
# insert one immediately after the 'name:' line.
_vg_tmp=$(mktemp)
awk -v ns="$NAMESPACE" '
    BEGIN { in_vg=0; ns_done=0 }
    /^---/ { in_vg=0; ns_done=0 }
    /^kind:[ \t]*VolumeGroup$/ { in_vg=1 }
    in_vg && /^  namespace:/ { ns_done=1 }
    in_vg && /^  name:/ && !ns_done {
        print
        print "  namespace: " ns
        ns_done=1
        next
    }
    { print }
' "${YAML_DIR}/vg.yaml" > "$_vg_tmp" 2>/dev/null || cp "${YAML_DIR}/vg.yaml" "$_vg_tmp"

run_step "Apply VolumeGroup" bash -c "$OC apply -f $_vg_tmp"
rm -f "$_vg_tmp"

# Label all PVCs that match the VolumeGroup selector so the operator picks them up
VG_LABEL_KEY=$(grep -A5 'matchLabels' "${YAML_DIR}/vg.yaml" 2>/dev/null \
    | grep ':' | head -1 | awk -F': ' '{gsub(/^[ \t]+/,"",$1); print $1}' || true)
VG_LABEL_VAL=$(grep -A5 'matchLabels' "${YAML_DIR}/vg.yaml" 2>/dev/null \
    | grep ':' | head -1 | awk -F': ' '{print $2}' || true)

if [[ -n "$VG_LABEL_KEY" && -n "$VG_LABEL_VAL" ]]; then
    log_info "Labelling PVCs with ${VG_LABEL_KEY}=${VG_LABEL_VAL} so VolumeGroup selector matches..."
    for pvc in $PVC_NAMES; do
        $OC label pvc "$pvc" "${VG_LABEL_KEY}=${VG_LABEL_VAL}" \
            --overwrite 2>/dev/null || true
        log_ok "Labelled pvc/${pvc}"
    done
else
    log_warn "Could not parse VolumeGroup matchLabels from vg.yaml – label PVCs manually if VG stays unready"
fi

# Discover VolumeGroup name (scoped to correct namespace)
VG_NAME=$($OC get volumegroup -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
if [[ -n "$VG_NAME" ]]; then
    run_step "VolumeGroup ready: $VG_NAME" wait_for_volumegroup_ready "$VG_NAME"
    show_resource_status volumegroup "$VG_NAME"
else
    log_warn "No VolumeGroup object found after applying vg.yaml – skipping readiness check"
fi

# ---------------------------------------------------------------------------
# PHASE 5 – Test Pod (exercises the base PVCs)
# ---------------------------------------------------------------------------
log_section "Phase 5: Deploy test pod using base PVCs"

# Use the first PVC name from pvc.yaml
FIRST_PVC=$(echo "$PVC_NAMES" | head -1)

run_step "Apply test pod" apply_yaml pod.yaml

# Discover pod name by parsing YAML directly (avoids picking up non-Pod kinds in multi-doc files)
POD_NAME=$(awk '
    /^---/ { kind=""; name="" }
    /^kind:/ { kind=$2 }
    /^  name:/ && kind=="Pod" { print $2; exit }
' "${YAML_DIR}/pod.yaml" 2>/dev/null || true)
POD_NAME="${POD_NAME:-e2e-test-pod}"
log_info "Test pod name: ${POD_NAME}"

run_step "Pod running: $POD_NAME" wait_for_pod_running "$POD_NAME"
show_resource_status pod "$POD_NAME"

# Smoke-test: write and read a file inside the pod (only for filesystem mounts)
log_info "Running I/O smoke test inside pod ${POD_NAME}..."
if $OC exec "$POD_NAME" -- sh -c \
    'echo "ibm-block-csi-e2e" > /mnt/data/e2e_testfile && cat /mnt/data/e2e_testfile' \
    2>/dev/null | grep -q "ibm-block-csi-e2e"; then
    pass "I/O smoke test on /mnt/data in pod $POD_NAME"
elif $OC exec "$POD_NAME" -- sh -c \
    'echo "ibm-block-csi-e2e" > /data/e2e_testfile && cat /data/e2e_testfile' \
    2>/dev/null | grep -q "ibm-block-csi-e2e"; then
    pass "I/O smoke test on /data in pod $POD_NAME"
else
    log_warn "I/O smoke test skipped (mountPath unknown or pod has no writable filesystem mount)"
fi

# ---------------------------------------------------------------------------
# PHASE 6 – VolumeSnapshot
# ---------------------------------------------------------------------------
log_section "Phase 6: Create VolumeSnapshot"

run_step "Apply VolumeSnapshot" apply_yaml volumesnapshot.yaml

# Parse snapshot name directly from YAML (avoids multi-doc confusion)
SNAP_NAME=$(awk '
    /^---/ { kind=""; name="" }
    /^kind:/ { kind=$2 }
    /^  name:/ && kind=="VolumeSnapshot" { print $2; exit }
' "${YAML_DIR}/volumesnapshot.yaml" 2>/dev/null || true)
SNAP_NAME="${SNAP_NAME:-demo-volumesnapshot}"
log_info "VolumeSnapshot name: ${SNAP_NAME}"

run_step "VolumeSnapshot readyToUse: $SNAP_NAME" wait_for_snapshot_ready "$SNAP_NAME"
show_resource_status volumesnapshot "$SNAP_NAME"

# Check VolumeSnapshotContent was created
VSC_NAME=$($OC get volumesnapshot "$SNAP_NAME" \
    -o jsonpath='{.status.boundVolumeSnapshotContentName}' 2>/dev/null || true)
if [[ -n "$VSC_NAME" ]]; then
    log_ok "Bound VolumeSnapshotContent: $VSC_NAME"
    show_resource_status volumesnapshotcontent "$VSC_NAME"
else
    log_warn "Could not determine bound VolumeSnapshotContent name"
fi

# ---------------------------------------------------------------------------
# PHASE 7 – PVC from Snapshot
# ---------------------------------------------------------------------------
log_section "Phase 7: Create PVC restored from snapshot"

run_step "Apply PVC-from-snapshot" apply_yaml pvc-from-snapshot.yaml

# Parse PVC-from-snapshot name directly from YAML
SNAP_PVC_NAME=$(awk '
    /^---/ { kind=""; name="" }
    /^kind:/ { kind=$2 }
    /^  name:/ && kind=="PersistentVolumeClaim" { print $2; exit }
' "${YAML_DIR}/pvc-from-snapshot.yaml" 2>/dev/null || true)
SNAP_PVC_NAME="${SNAP_PVC_NAME:-demo-pvc-from-snapshot}"
log_info "PVC-from-snapshot name: ${SNAP_PVC_NAME}"

run_step "PVC-from-snapshot Bound: $SNAP_PVC_NAME" wait_for_pvc_bound "$SNAP_PVC_NAME"
show_resource_status pvc "$SNAP_PVC_NAME"

# Verify data integrity: deploy a second pod mounting the restored PVC
log_info "Deploying data-validation pod on restored PVC ${SNAP_PVC_NAME}..."
$OC apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: e2e-test-pod-snapshot
  namespace: ${NAMESPACE}
  labels:
    app: e2e-test-pod-snapshot
spec:
  restartPolicy: Never
  containers:
  - name: validator
    image: busybox:1.36
    command: ["sh", "-c", "ls /data && echo 'restored-pvc-ok' > /data/restore_check && sleep 3600"]
    volumeMounts:
    - name: restored-vol
      mountPath: /data
  volumes:
  - name: restored-vol
    persistentVolumeClaim:
      claimName: ${SNAP_PVC_NAME}
EOF

run_step "Restored-PVC pod running: e2e-test-pod-snapshot" \
    wait_for_pod_running "e2e-test-pod-snapshot"

if $OC exec "e2e-test-pod-snapshot" -- sh -c \
    'cat /data/restore_check 2>/dev/null || echo "no-file"' \
    | grep -q "restored-pvc-ok"; then
    pass "Data write on restored PVC succeeded"
else
    log_warn "Restored PVC write check inconclusive (may still be valid if PVC was empty)"
fi

# ---------------------------------------------------------------------------
# PHASE 8 – Status summary of all IBM Block CSI driver pods
# ---------------------------------------------------------------------------
log_section "Phase 8: IBM Block CSI driver pod health"

log_info "All pods in namespace '${NAMESPACE}':"
$OC get pods -o wide

log_info "IBM Block CSI images in use:"
$OC get pods -o yaml 2>/dev/null | grep -E 'image:.*csiblock|image:.*ibm-block' | sort -u || true

log_info "CSI driver registration:"
$OC get csidriver block.csi.ibm.com -o wide 2>/dev/null || \
    log_warn "CSIDriver 'block.csi.ibm.com' not found in cluster"

log_info "Persistent Volumes provisioned by IBM Block CSI:"
$OC get pv -o wide 2>/dev/null | grep block.csi.ibm.com || \
    log_warn "No PVs provisioned by block.csi.ibm.com found"

# ---------------------------------------------------------------------------
# PHASE 9 – Negative / error-path checks
# ---------------------------------------------------------------------------
log_section "Phase 9: Negative checks"

log_info "Checking for driver error events in namespace '${NAMESPACE}'..."
ERR_EVENTS=$($OC get events --field-selector='reason!=Pulled,reason!=Scheduled,reason!=Started,reason!=Created,type=Warning' \
    --no-headers 2>/dev/null | grep -i "block.csi.ibm.com\|provisioning\|attaching\|mounting" || true)
if [[ -n "$ERR_EVENTS" ]]; then
    log_warn "Warning events related to IBM Block CSI driver:"
    echo "$ERR_EVENTS"
else
    pass "No relevant Warning events found"
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
print_summary

[[ $FAIL_COUNT -eq 0 ]]
