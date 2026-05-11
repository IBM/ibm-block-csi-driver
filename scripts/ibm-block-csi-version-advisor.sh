#!/bin/bash
# ==============================================================================
# IBM Block CSI Driver - Version Upgrade Advisor Script
# ==============================================================================
# This script checks the current environment and provides recommendations
# for upgrading to the best supported or latest version of IBM Block CSI Driver.
#
# It checks:
# - Current CSI driver version
# - Orchestration platform (Kubernetes or OpenShift)
# - Operating system (for Kubernetes clusters)
# - Provides upgrade recommendations based on support matrix
# ==============================================================================

set -o pipefail

VERSION="1.0.0"
SCRIPT_NAME="$(basename "$0")"

# Color codes for output
readonly RED='\033[0;31m'
readonly YELLOW='\033[1;33m'
readonly GREEN='\033[0;32m'
readonly BLUE='\033[0;34m'
readonly CYAN='\033[0;36m'
readonly NC='\033[0m' # No Color
readonly BOLD='\033[1m'

# Global variables
CSI_VERSION=""
PLATFORM=""
PLATFORM_VERSION=""
OS_TYPE=""
OS_VERSION=""
AUTO_DETECT=true
VERBOSE=false

# Supported versions (as per requirements)
readonly SUPPORTED_CSI_VERSIONS=("1.12.5" "1.13.0" "1.13.1")
readonly LATEST_CSI_VERSION="1.13.1"
readonly RECOMMENDED_CSI_VERSION="1.13.1"

# Minimum supported versions
readonly MIN_CSI_VERSION="1.12.5"
readonly MIN_OCP_VERSION="4.18"
readonly MIN_UBUNTU_VERSION="22.04"
readonly MIN_RHEL_VERSION="9.7"

# Supported OCP versions
readonly SUPPORTED_OCP_VERSIONS=("4.18" "4.19" "4.20", "4.21")

# Supported OS versions for Kubernetes
readonly SUPPORTED_UBUNTU_VERSIONS=("22.04" "24.04")
readonly SUPPORTED_RHEL_VERSIONS=("9.7")

# ==============================================================================
# Utility Functions
# ==============================================================================

print_header() {
    echo -e "${BOLD}${BLUE}"
    echo "================================================================================"
    echo "  IBM Block CSI Driver - Version Upgrade Advisor v${VERSION}"
    echo "================================================================================"
    echo -e "${NC}"
}

print_section() {
    echo ""
    echo -e "${BOLD}${CYAN}$1${NC}"
    echo "--------------------------------------------------------------------------------"
}

print_success() {
    echo -e "${GREEN}✓${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}⚠${NC} $1"
}

print_error() {
    echo -e "${RED}✗${NC} $1"
}

print_info() {
    echo -e "${BLUE}ℹ${NC} $1"
}

log_verbose() {
    if [ "$VERBOSE" = true ]; then
        echo -e "${CYAN}[DEBUG]${NC} $1"
    fi
}

usage() {
    cat << EOF
Usage: $SCRIPT_NAME [OPTIONS]

IBM Block CSI Driver Version Upgrade Advisor - Checks environment and recommends upgrades

OPTIONS:
    -c, --csi-version VERSION        Current CSI driver version (e.g., 1.11.0)
    -p, --platform TYPE              Platform type (k8s|ocp)
    -v, --platform-version VERSION   Platform version (e.g., 1.31 for K8s, 4.17 for OCP)
    --os-type TYPE                   OS type for K8s (ubuntu|rhel)
    --os-version VERSION             OS version (e.g., 22.04, 9.7)
    --no-auto-detect                 Disable automatic detection
    --verbose                        Enable verbose output
    -h, --help                       Display this help message

EXAMPLES:
    # Auto-detect current environment
    $SCRIPT_NAME

    # Check specific CSI version with OpenShift
    $SCRIPT_NAME --csi-version 1.11.0 --platform ocp --platform-version 4.17

    # Check Kubernetes with Ubuntu
    $SCRIPT_NAME --csi-version 1.12.0 --platform k8s --platform-version 1.31 \\
                 --os-type ubuntu --os-version 22.04

    # Check Kubernetes with RHEL
    $SCRIPT_NAME --csi-version 1.12.3 --platform k8s --platform-version 1.32 \\
                 --os-type rhel --os-version 9.7

SUPPORTED VERSIONS:
    CSI Driver: 1.12.5, 1.13.0, 1.13.1
    OpenShift: 4.18-4.20
    Ubuntu: 22.04, 24.04
    RHEL: 9.7

EOF
    exit 0
}

# ==============================================================================
# Detection Functions
# ==============================================================================

detect_csi_version() {
    log_verbose "Detecting current CSI driver version..."
    
    if ! command -v kubectl &> /dev/null; then
        log_verbose "kubectl not available"
        return 1
    fi
    
    # Method 1: Try to get version from IBMBlockCSI custom resource (for operator-based deployments)
    local version=$(kubectl get ibmblockcsis.csi.ibm.com -A -o jsonpath='{.items[0].status.version}' 2>/dev/null)
    
    if [ -n "$version" ]; then
        CSI_VERSION="$version"
        log_verbose "Detected CSI version from IBMBlockCSI CR: $version"
        return 0
    fi
    
    # Method 2: Try to get version from deployment in kube-system
    version=$(kubectl get deployment -n kube-system ibm-block-csi-controller -o jsonpath='{.spec.template.spec.containers[?(@.name=="ibm-block-csi-controller")].image}' 2>/dev/null | grep -oP 'v\K[0-9]+\.[0-9]+\.[0-9]+' | head -1)
    
    if [ -n "$version" ]; then
        CSI_VERSION="$version"
        log_verbose "Detected CSI version from deployment: $version"
        return 0
    fi
    
    # Method 3: Try to get version from deployment in openshift-storage (for ODF)
    version=$(kubectl get deployment -n openshift-storage ibm-block-csi-controller -o jsonpath='{.spec.template.spec.containers[?(@.name=="ibm-block-csi-controller")].image}' 2>/dev/null | grep -oP 'v\K[0-9]+\.[0-9]+\.[0-9]+' | head -1)
    
    if [ -n "$version" ]; then
        CSI_VERSION="$version"
        log_verbose "Detected CSI version from openshift-storage: $version"
        return 0
    fi
    
    log_verbose "Could not auto-detect CSI version"
    return 1
}

detect_platform() {
    log_verbose "Detecting platform..."
    
    if ! command -v kubectl &> /dev/null; then
        log_verbose "kubectl not available"
        return 1
    fi
    
    # Check if OpenShift
    if kubectl get clusterversion &> /dev/null 2>&1; then
        PLATFORM="ocp"
        PLATFORM_VERSION=$(kubectl get clusterversion -o jsonpath='{.items[0].status.desired.version}' 2>/dev/null | grep -oP '^[0-9]+\.[0-9]+')
        log_verbose "Detected OpenShift version: $PLATFORM_VERSION"
        return 0
    else
        PLATFORM="k8s"
        # Try multiple methods to get Kubernetes version
        
        # Method 1: kubectl version (newer format)
        local version=$(kubectl version -o json 2>/dev/null | grep -oP '"serverVersion":\s*\{\s*"major":\s*"\K[0-9]+' | head -1)
        local minor=$(kubectl version -o json 2>/dev/null | grep -oP '"minor":\s*"\K[0-9]+' | head -1)
        
        if [ -n "$version" ] && [ -n "$minor" ]; then
            PLATFORM_VERSION="${version}.${minor}"
            log_verbose "Detected Kubernetes version: $PLATFORM_VERSION"
            return 0
        fi
        
        # Method 2: kubectl version --short (older format)
        version=$(kubectl version --short 2>/dev/null | grep "Server Version" | grep -oP 'v\K[0-9]+\.[0-9]+')
        
        if [ -n "$version" ]; then
            PLATFORM_VERSION="$version"
            log_verbose "Detected Kubernetes version: $PLATFORM_VERSION"
            return 0
        fi
        
        # Method 3: kubectl version (standard output parsing)
        version=$(kubectl version 2>/dev/null | grep "Server Version" | grep -oP 'v\K[0-9]+\.[0-9]+' | head -1)
        
        if [ -n "$version" ]; then
            PLATFORM_VERSION="$version"
            log_verbose "Detected Kubernetes version: $PLATFORM_VERSION"
            return 0
        fi
        
        log_verbose "Kubernetes detected but version could not be determined"
        return 1
    fi
}

detect_os() {
    log_verbose "Detecting operating system for Kubernetes cluster..."
    
    # Only detect OS for Kubernetes clusters
    if [ "$PLATFORM" != "k8s" ]; then
        log_verbose "Not a Kubernetes cluster, skipping OS detection"
        return 0
    fi
    
    if ! command -v kubectl &> /dev/null; then
        log_verbose "kubectl not available, cannot detect OS"
        return 1
    fi
    
    # Get a sample node
    local sample_node=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
    
    if [ -z "$sample_node" ]; then
        log_verbose "Could not get node information"
        return 1
    fi
    
    log_verbose "Checking OS on node: $sample_node"
    
    # Use kubectl debug to check OS on the node
    local os_info=$(kubectl debug "node/$sample_node" \
                --profile=sysadmin \
                --image=registry.access.redhat.com/ubi9/ubi:latest \
                --attach=true \
                -- chroot /host bash -c 'cat /etc/os-release' 2>/dev/null | grep -E "^(ID=|VERSION_ID=)")
    
    if [ -n "$os_info" ]; then
        # Parse OS information
        OS_TYPE=$(echo "$os_info" | grep "^ID=" | cut -d= -f2 | tr -d '"' | tr '[:upper:]' '[:lower:]')
        OS_VERSION=$(echo "$os_info" | grep "^VERSION_ID=" | cut -d= -f2 | tr -d '"')
        
        # Handle RHCOS (treat as RHEL for compatibility)
        if [[ "$OS_TYPE" == "rhcos" ]] || [[ "$OS_TYPE" == "rhel" ]]; then
            OS_TYPE="rhel"
        fi
        
        log_verbose "Detected OS: $OS_TYPE $OS_VERSION"
        return 0
    fi
    
    log_verbose "Could not auto-detect OS from node"
    return 1
}

# ==============================================================================
# Version Comparison Functions
# ==============================================================================

version_compare() {
    # Compare two version strings
    # Returns: 0 if equal, 1 if $1 > $2, 2 if $1 < $2
    local v1=$1
    local v2=$2
    
    if [ "$v1" = "$v2" ]; then
        return 0
    fi
    
    local IFS=.
    local i ver1=($v1) ver2=($v2)
    
    for ((i=0; i<${#ver1[@]} || i<${#ver2[@]}; i++)); do
        local num1=${ver1[i]:-0}
        local num2=${ver2[i]:-0}
        
        if [ "$num1" -gt "$num2" ]; then
            return 1
        elif [ "$num1" -lt "$num2" ]; then
            return 2
        fi
    done
    
    return 0
}

is_version_supported() {
    local version=$1
    shift
    local supported_array=("$@")
    
    for v in "${supported_array[@]}"; do
        if [ "$version" = "$v" ]; then
            return 0
        fi
    done
    return 1
}

# ==============================================================================
# Validation Functions
# ==============================================================================

check_csi_version() {
    print_section "CSI Driver Version Check"
    
    print_info "Current CSI Version: $CSI_VERSION"
    print_info "Minimum Supported Version: $MIN_CSI_VERSION"
    print_info "Latest Version: $LATEST_CSI_VERSION"
    
    version_compare "$CSI_VERSION" "$MIN_CSI_VERSION"
    local cmp=$?
    
    if [ $cmp -eq 2 ]; then
        print_error "CSI version $CSI_VERSION is below minimum supported version $MIN_CSI_VERSION"
        return 1
    elif is_version_supported "$CSI_VERSION" "${SUPPORTED_CSI_VERSIONS[@]}"; then
        print_success "CSI version $CSI_VERSION is currently supported"
        return 0
    else
        print_warning "CSI version $CSI_VERSION is not in the latest supported versions"
        return 2
    fi
}

check_platform_version() {
    print_section "Platform Version Check"
    
    local issues=0
    
    if [ "$PLATFORM" = "ocp" ]; then
        print_info "Platform: OpenShift $PLATFORM_VERSION"
        print_info "Minimum Supported OCP Version: $MIN_OCP_VERSION"
        print_info "Supported OCP Versions: ${SUPPORTED_OCP_VERSIONS[*]}"
        
        version_compare "$PLATFORM_VERSION" "$MIN_OCP_VERSION"
        local cmp=$?
        
        if [ $cmp -eq 2 ]; then
            print_error "OpenShift version $PLATFORM_VERSION is below minimum supported version $MIN_OCP_VERSION"
            ((issues++))
        elif is_version_supported "$PLATFORM_VERSION" "${SUPPORTED_OCP_VERSIONS[@]}"; then
            print_success "OpenShift version $PLATFORM_VERSION is supported"
        else
            print_warning "OpenShift version $PLATFORM_VERSION may not be fully supported"
            print_info "Supported versions: ${SUPPORTED_OCP_VERSIONS[*]}"
            ((issues++))
        fi
    elif [ "$PLATFORM" = "k8s" ]; then
        print_info "Platform: Kubernetes $PLATFORM_VERSION"
        print_success "Kubernetes version detected"
    else
        print_error "Unknown platform: $PLATFORM"
        ((issues++))
    fi
    
    return $issues
}

check_os_version() {
    print_section "Operating System Check"
    
    if [ "$PLATFORM" = "ocp" ]; then
        print_info "OpenShift uses RHCOS - OS check not required"
        return 0
    fi
    
    if [ -z "$OS_TYPE" ] || [ -z "$OS_VERSION" ]; then
        print_warning "OS information not available"
        print_info "For Kubernetes, OS must be Ubuntu 22.04/24.04 or RHEL 9.7"
        return 1
    fi
    
    local issues=0
    
    print_info "Operating System: $OS_TYPE $OS_VERSION"
    
    if [ "$OS_TYPE" = "ubuntu" ]; then
        print_info "Minimum Ubuntu Version: $MIN_UBUNTU_VERSION"
        print_info "Supported Ubuntu Versions: ${SUPPORTED_UBUNTU_VERSIONS[*]}"
        
        version_compare "$OS_VERSION" "$MIN_UBUNTU_VERSION"
        local cmp=$?
        
        if [ $cmp -eq 2 ]; then
            print_error "Ubuntu version $OS_VERSION is below minimum supported version $MIN_UBUNTU_VERSION"
            ((issues++))
        elif is_version_supported "$OS_VERSION" "${SUPPORTED_UBUNTU_VERSIONS[@]}"; then
            print_success "Ubuntu version $OS_VERSION is supported"
        else
            print_warning "Ubuntu version $OS_VERSION may not be fully supported"
            print_info "Supported versions: ${SUPPORTED_UBUNTU_VERSIONS[*]}"
            ((issues++))
        fi
    elif [ "$OS_TYPE" = "rhel" ]; then
        print_info "Minimum RHEL Version: $MIN_RHEL_VERSION"
        print_info "Supported RHEL Versions: ${SUPPORTED_RHEL_VERSIONS[*]}"
        
        version_compare "$OS_VERSION" "$MIN_RHEL_VERSION"
        local cmp=$?
        
        if [ $cmp -eq 2 ]; then
            print_error "RHEL version $OS_VERSION is below minimum supported version $MIN_RHEL_VERSION"
            ((issues++))
        elif is_version_supported "$OS_VERSION" "${SUPPORTED_RHEL_VERSIONS[@]}"; then
            print_success "RHEL version $OS_VERSION is supported"
        else
            print_warning "RHEL version $OS_VERSION may not be fully supported"
            print_info "Supported versions: ${SUPPORTED_RHEL_VERSIONS[*]}"
            ((issues++))
        fi
    else
        print_error "Unsupported OS type: $OS_TYPE"
        print_info "Only Ubuntu and RHEL are supported for Kubernetes"
        ((issues++))
    fi
    
    return $issues
}

# ==============================================================================
# Recommendation Functions
# ==============================================================================

generate_recommendations() {
    local csi_status=$1
    local platform_issues=$2
    local os_issues=$3
    
    print_section "Upgrade Recommendations"
    
    local needs_upgrade=false
    local can_upgrade=true
    
    # Determine if upgrade is needed
    if [ $csi_status -eq 1 ] || [ $csi_status -eq 2 ]; then
        needs_upgrade=true
    fi
    
    # Determine if upgrade is possible
    if [ $platform_issues -gt 0 ] || [ $os_issues -gt 0 ]; then
        can_upgrade=false
    fi
    
    echo ""
    
    if [ "$needs_upgrade" = false ]; then
        print_success "Your CSI driver version is up to date!"
        echo ""
        echo "Current version: $CSI_VERSION"
        echo "Latest version: $LATEST_CSI_VERSION"
        echo ""
        print_info "No upgrade required at this time"
        return 0
    fi
    
    if [ "$can_upgrade" = false ]; then
        print_error "Upgrade is REQUIRED but environment needs updates first"
        echo ""
        echo "REQUIRED ACTIONS:"
        echo ""
        
        if [ $platform_issues -gt 0 ]; then
            if [ "$PLATFORM" = "ocp" ]; then
                echo "  1. Upgrade OpenShift to version $MIN_OCP_VERSION or higher"
                echo "     Supported versions: ${SUPPORTED_OCP_VERSIONS[*]}"
            fi
        fi
        
        if [ $os_issues -gt 0 ]; then
            if [ "$OS_TYPE" = "ubuntu" ]; then
                echo "  2. Upgrade Ubuntu to version $MIN_UBUNTU_VERSION or higher"
                echo "     Supported versions: ${SUPPORTED_UBUNTU_VERSIONS[*]}"
            elif [ "$OS_TYPE" = "rhel" ]; then
                echo "  2. Upgrade RHEL to version $MIN_RHEL_VERSION or higher"
                echo "     Supported versions: ${SUPPORTED_RHEL_VERSIONS[*]}"
            fi
        fi
        
        echo ""
        echo "After addressing the above requirements, upgrade CSI driver to:"
        echo "  • Recommended: $RECOMMENDED_CSI_VERSION (latest stable)"
        echo "  • Minimum: $MIN_CSI_VERSION (minimum supported)"
        
    else
        print_warning "Upgrade is RECOMMENDED"
        echo ""
        echo "Current CSI Version: $CSI_VERSION"
        echo ""
        echo "UPGRADE OPTIONS:"
        echo ""
        echo "  Option 1 (RECOMMENDED): Upgrade to Latest Version"
        echo "  ────────────────────────────────────────────────"
        echo "    Target Version: $LATEST_CSI_VERSION"
        echo "    Benefits:"
        echo "      • Latest features and improvements"
        echo "      • Latest security patches"
        echo "      • Extended support lifecycle"
        echo "      • Best performance and stability"
        echo ""
        echo "  Option 2: Upgrade to Minimum Supported Version"
        echo "  ───────────────────────────────────────────────"
        echo "    Target Version: $MIN_CSI_VERSION"
        echo "    Benefits:"
        echo "      • Maintains current support"
        echo "      • Smaller upgrade step"
        echo "      • Lower risk if conservative approach needed"
        echo ""
        
        print_info "We strongly recommend Option 1 (upgrade to $LATEST_CSI_VERSION)"
    fi
    
    echo ""
    echo "NEXT STEPS:"
    echo "───────────"
    echo "  1. Review release notes for target version"
    echo "  2. Backup current configuration and PV/PVC definitions"
    echo "  3. Test upgrade in non-production environment"
    echo "  4. Plan maintenance window"
    echo "  5. Follow upgrade procedure from documentation"
    echo ""
    echo "RESOURCES:"
    echo "──────────"
    echo "  • Documentation: https://www.ibm.com/docs/en/stg-block-csi-driver"
    echo "  • Release Notes: https://github.com/IBM/ibm-block-csi-driver/releases"
    echo "  • Support: https://www.ibm.com/mysupport"
    echo ""
}

# ==============================================================================
# Main Function
# ==============================================================================

main() {
    print_header
    
    # Auto-detection phase
    if [ "$AUTO_DETECT" = true ]; then
        print_section "Auto-detecting Environment"
        
        detect_csi_version || print_warning "Could not detect CSI version"
        detect_platform || print_warning "Could not detect platform"
        
        if [ "$PLATFORM" = "k8s" ]; then
            detect_os || print_warning "Could not detect OS"
        fi
        
        echo ""
        print_info "CSI Version: ${CSI_VERSION:-Not detected}"
        print_info "Platform: ${PLATFORM:-Not detected} ${PLATFORM_VERSION:-}"
        if [ "$PLATFORM" = "k8s" ]; then
            print_info "Operating System: ${OS_TYPE:-Not detected} ${OS_VERSION:-}"
        fi
    fi
    
    # Validation and intelligent suggestions
    local can_proceed=true
    local csi_status=0
    local platform_issues=0
    local os_issues=0
    
    if [ -z "$CSI_VERSION" ]; then
        print_warning "CSI version not detected"
        print_info "Unable to determine current CSI driver version"
        print_info "Recommendation: Upgrade to latest supported version: $LATEST_CSI_VERSION"
        can_proceed=false
    fi
    
    if [ -z "$PLATFORM" ] || [ -z "$PLATFORM_VERSION" ]; then
        print_warning "Platform information not detected"
        print_info "Unable to determine Kubernetes/OpenShift version"
        print_info "Supported platforms:"
        print_info "  • OpenShift: ${SUPPORTED_OCP_VERSIONS[*]}"
        print_info "  • Kubernetes: Various versions"
        can_proceed=false
    fi
    
    if [ "$can_proceed" = false ]; then
        print_section "General Recommendations"
        echo ""
        echo "Since environment details could not be auto-detected, here are general recommendations:"
        echo ""
        echo "RECOMMENDED CSI DRIVER VERSION:"
        echo "  • Latest: $LATEST_CSI_VERSION (recommended)"
        echo "  • Minimum Supported: $MIN_CSI_VERSION"
        echo ""
        echo "SUPPORTED PLATFORMS:"
        echo "  • OpenShift: ${SUPPORTED_OCP_VERSIONS[*]}"
        echo "  • Kubernetes with Ubuntu: ${SUPPORTED_UBUNTU_VERSIONS[*]}"
        echo "  • Kubernetes with RHEL: ${SUPPORTED_RHEL_VERSIONS[*]}"
        echo ""
        echo "UPGRADE CHECKLIST:"
        echo "  1. Verify your current CSI driver version"
        echo "  2. Check your platform version (K8s/OCP)"
        echo "  3. For Kubernetes: Verify OS version (Ubuntu/RHEL)"
        echo "  4. If any component is below minimum supported version, upgrade it first"
        echo "  5. Then upgrade CSI driver to $LATEST_CSI_VERSION"
        echo ""
        echo "RESOURCES:"
        echo "  • Documentation: https://www.ibm.com/docs/en/stg-block-csi-driver"
        echo "  • Release Notes: https://github.com/IBM/ibm-block-csi-driver/releases"
        echo "  • Support: https://www.ibm.com/mysupport"
        echo ""
        print_info "Run this script with detected values for detailed analysis"
        exit 0
    fi
    
    # Run checks if we have the required information
    check_csi_version
    csi_status=$?
    
    check_platform_version
    platform_issues=$?
    
    if [ "$PLATFORM" = "k8s" ]; then
        if [ -z "$OS_TYPE" ] || [ -z "$OS_VERSION" ]; then
            print_warning "OS information not detected for Kubernetes cluster"
            print_info "For complete validation, OS must be Ubuntu 22.04/24.04 or RHEL 9.7"
            os_issues=0
        else
            check_os_version
            os_issues=$?
        fi
    else
        check_os_version
        os_issues=$?
    fi
    
    # Generate recommendations
    generate_recommendations $csi_status $platform_issues $os_issues
    
    # Summary
    print_section "Summary"
    
    local total_issues=$((platform_issues + os_issues))
    
    if [ $csi_status -eq 0 ] && [ $total_issues -eq 0 ]; then
        print_success "Environment check: PASSED"
        echo "Your environment is compatible with current supported versions"
        exit 0
    elif [ $total_issues -gt 0 ]; then
        print_error "Environment check: FAILED"
        echo "Found $total_issues compatibility issue(s) that must be resolved"
        exit 1
    else
        print_warning "Environment check: UPGRADE RECOMMENDED"
        echo "Your environment is compatible but CSI driver upgrade is recommended"
        exit 0
    fi
}

# ==============================================================================
# Argument Parsing
# ==============================================================================

while [[ $# -gt 0 ]]; do
    case $1 in
        -c|--csi-version)
            CSI_VERSION="$2"
            shift 2
            ;;
        -p|--platform)
            PLATFORM="$2"
            shift 2
            ;;
        -v|--platform-version)
            PLATFORM_VERSION="$2"
            shift 2
            ;;
        --os-type)
            OS_TYPE="$2"
            shift 2
            ;;
        --os-version)
            OS_VERSION="$2"
            shift 2
            ;;
        --no-auto-detect)
            AUTO_DETECT=false
            shift
            ;;
        --verbose)
            VERBOSE=true
            shift
            ;;
        -h|--help)
            usage
            ;;
        *)
            echo "Unknown option: $1"
            usage
            ;;
    esac
done

# Run main function
main

# Made with Bob
