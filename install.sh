#!/bin/bash
set -euo pipefail

REPO="phillarmonic/watchur"
BINARY_NAME="watchur"
INSTALL_DIR="${INSTALL_DIR:-}"
GITHUB_API="https://api.github.com/repos/${REPO}"
GITHUB_RELEASES="https://github.com/${REPO}/releases"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() {
    echo -e "${BLUE}ℹ️  $1${NC}"
}

log_success() {
    echo -e "${GREEN}✅ $1${NC}"
}

log_warn() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

log_error() {
    echo -e "${RED}❌ $1${NC}" >&2
}

cleanup() {
    if [[ -n "${TEMP_DIR:-}" ]] && [[ -d "${TEMP_DIR}" ]]; then
        rm -rf "${TEMP_DIR}"
    fi
}

trap cleanup EXIT

check_platform() {
    local os arch

    case "$(uname -s)" in
        Linux*)
            os="linux"
            ;;
        Darwin*)
            os="darwin"
            ;;
        MINGW*|MSYS*|CYGWIN*)
            os="windows"
            ;;
        *)
            log_error "Unsupported operating system: $(uname -s)"
            log_error "Supported platforms: Linux, macOS, Windows"
            exit 1
            ;;
    esac

    case "$(uname -m)" in
        x86_64|amd64)
            arch="amd64"
            ;;
        arm64|aarch64)
            arch="arm64"
            ;;
        *)
            log_error "Unsupported architecture: $(uname -m)"
            log_error "Supported architectures: amd64, arm64"
            exit 1
            ;;
    esac

    PLATFORM_OS="${os}"
    PLATFORM_ARCH="${arch}"

    if [[ -z "${INSTALL_DIR}" ]]; then
        if [[ "${os}" == "windows" ]]; then
            INSTALL_DIR="$HOME/bin"
        else
            INSTALL_DIR="$HOME/.local/bin"
        fi
    fi

    if [[ "${os}" == "windows" ]]; then
        RELEASE_BINARY="${BINARY_NAME}-${os}-${arch}.exe"
        INSTALLED_BINARY="${BINARY_NAME}.exe"
    else
        RELEASE_BINARY="${BINARY_NAME}-${os}-${arch}"
        INSTALLED_BINARY="${BINARY_NAME}"
    fi

    log_info "Detected platform: ${PLATFORM_OS}/${PLATFORM_ARCH}"
}

check_dependencies() {
    local missing=()

    for cmd in curl; do
        if ! command -v "${cmd}" >/dev/null 2>&1; then
            missing+=("${cmd}")
        fi
    done

    if [[ ${#missing[@]} -gt 0 ]]; then
        log_error "Missing required dependencies: ${missing[*]}"
        exit 1
    fi
}

get_latest_version() {
    log_info "Fetching latest release information..." >&2

    local response version
    if ! response=$(curl -sSf "${GITHUB_API}/releases/latest" 2>/dev/null); then
        log_error "Failed to fetch release information from GitHub"
        exit 1
    fi

    if command -v jq >/dev/null 2>&1; then
        version=$(echo "${response}" | jq -r '.tag_name')
    else
        version=$(echo "${response}" | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' | cut -d'"' -f4)
    fi

    if [[ -z "${version}" || "${version}" == "null" ]]; then
        log_error "Failed to determine the latest release version"
        exit 1
    fi

    echo "${version}"
}

validate_version() {
    local version="$1"

    if [[ ! "${version}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?$ ]]; then
        log_error "Invalid version format: ${version}"
        log_error "Expected format: v1.0.0 or v1.0.0-beta.1"
        exit 1
    fi
}

check_version_exists() {
    local version="$1"

    log_info "Checking if version ${version} exists..."
    if ! curl -sSf "${GITHUB_API}/releases/tags/${version}" >/dev/null 2>&1; then
        log_error "Version ${version} not found"
        log_error "Available releases: ${GITHUB_RELEASES}"
        exit 1
    fi

    log_success "Version ${version} found"
}

install_binary() {
    local version="$1"
    local download_url="https://github.com/${REPO}/releases/download/${version}/${RELEASE_BINARY}"

    TEMP_DIR=$(mktemp -d)
    local temp_binary="${TEMP_DIR}/${INSTALLED_BINARY}"

    log_info "Downloading ${RELEASE_BINARY}..."
    log_info "URL: ${download_url}"
    if ! curl -sSfL "${download_url}" -o "${temp_binary}"; then
        log_error "Failed to download ${RELEASE_BINARY}"
        log_error "Check the release page: ${GITHUB_RELEASES}/tag/${version}"
        exit 1
    fi

    chmod +x "${temp_binary}"

    log_info "Verifying binary..."
    if ! "${temp_binary}" --version >/dev/null 2>&1; then
        log_error "Downloaded binary failed verification"
        exit 1
    fi

    if [[ ! -d "${INSTALL_DIR}" ]]; then
        log_info "Creating install directory: ${INSTALL_DIR}"
        mkdir -p "${INSTALL_DIR}"
    fi

    local target="${INSTALL_DIR}/${INSTALLED_BINARY}"
    log_info "Installing to ${target}"
    cp "${temp_binary}" "${target}"
    chmod +x "${target}"

    log_success "Installed ${BINARY_NAME} ${version} to ${target}"

    case ":$PATH:" in
        *":${INSTALL_DIR}:"*)
            log_success "${INSTALL_DIR} is already on PATH"
            ;;
        *)
            log_warn "${INSTALL_DIR} is not on PATH"
            if [[ "${PLATFORM_OS}" == "windows" ]]; then
                log_warn "Add ${INSTALL_DIR} to PATH to run ${INSTALLED_BINARY} from a new shell"
            else
                log_warn "Add this line to your shell profile:"
                echo "export PATH=\"${INSTALL_DIR}:\$PATH\""
            fi
            ;;
    esac

    log_info "Run '${INSTALLED_BINARY} --version' to verify the installation"
}

main() {
    check_platform
    check_dependencies

    local version="${1:-}"
    if [[ -z "${version}" ]]; then
        version=$(get_latest_version)
        log_info "Latest version: ${version}"
    else
        validate_version "${version}"
        check_version_exists "${version}"
    fi

    install_binary "${version}"
}

main "$@"
