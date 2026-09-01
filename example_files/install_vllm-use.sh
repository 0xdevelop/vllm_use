#!/usr/bin/env bash

set -Eeuo pipefail

APP_NAME="vllm-use"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
INSTALL_ROOT="${VLLM_USE_INSTALL_ROOT:-}"
SYSTEMCTL="${SYSTEMCTL:-systemctl}"

prefix_path() {
    printf '%s%s' "${INSTALL_ROOT%/}" "$1"
}

BIN_SOURCE="${SCRIPT_DIR}/${APP_NAME}"
SERVICE_SOURCE="${SCRIPT_DIR}/${APP_NAME}.service"
ENV_SOURCE="${SCRIPT_DIR}/${APP_NAME}.env"
BIN_TARGET="$(prefix_path "/usr/local/bin/${APP_NAME}")"
SERVICE_TARGET="$(prefix_path "/etc/systemd/system/${APP_NAME}.service")"
ENV_TARGET="$(prefix_path "/etc/${APP_NAME}/${APP_NAME}.env")"
STATE_DIR="$(prefix_path "/var/lib/${APP_NAME}")"
CACHE_DIR="$(prefix_path "/var/cache/${APP_NAME}")"

usage() {
    cat >&2 <<'USAGE'
Usage: install_vllm-use.sh install|update|uninstall

Install/update expects vllm-use, vllm-use.service and vllm-use.env next to this script.
Uninstall removes program and service files but preserves configuration, models,
SQLite data and the service account.
USAGE
}

require_root() {
    if [[ -z "${INSTALL_ROOT}" && "${EUID}" -ne 0 ]]; then
        echo "error: run as root (or set VLLM_USE_INSTALL_ROOT for packaging tests)" >&2
        exit 1
    fi
}

require_payload() {
    local file
    for file in "${BIN_SOURCE}" "${SERVICE_SOURCE}" "${ENV_SOURCE}"; do
        if [[ ! -f "${file}" ]]; then
            echo "error: missing installation payload: ${file}" >&2
            exit 1
        fi
    done
    if [[ ! -x "${BIN_SOURCE}" ]]; then
        echo "error: binary is not executable: ${BIN_SOURCE}" >&2
        exit 1
    fi
}

ensure_service_account() {
    [[ -n "${INSTALL_ROOT}" ]] && return
    if ! getent passwd "${APP_NAME}" >/dev/null; then
        useradd --system --home-dir "/var/lib/${APP_NAME}" --create-home --shell /usr/sbin/nologin "${APP_NAME}"
    fi

    local group
    for group in video render; do
        if getent group "${group}" >/dev/null; then
            usermod --append --groups "${group}" "${APP_NAME}"
        fi
    done
}

install_files() {
    require_payload
    ensure_service_account

    install -D -m 0755 "${BIN_SOURCE}" "${BIN_TARGET}"
    install -D -m 0644 "${SERVICE_SOURCE}" "${SERVICE_TARGET}"
    install -d -m 0750 "$(dirname -- "${ENV_TARGET}")" "${STATE_DIR}" "${CACHE_DIR}"
    if [[ ! -e "${ENV_TARGET}" ]]; then
        install -m 0600 "${ENV_SOURCE}" "${ENV_TARGET}"
    fi

    if [[ -z "${INSTALL_ROOT}" ]]; then
        chown -R "${APP_NAME}:${APP_NAME}" "${STATE_DIR}" "${CACHE_DIR}"
        "${SYSTEMCTL}" daemon-reload
    fi
}

install_service() {
    install_files
    if [[ -z "${INSTALL_ROOT}" ]]; then
        "${SYSTEMCTL}" enable --now "${APP_NAME}.service"
    fi
    echo "installed ${APP_NAME}; persistent data: ${STATE_DIR}; configuration: ${ENV_TARGET}"
}

update_service() {
    install_files
    if [[ -z "${INSTALL_ROOT}" ]]; then
        "${SYSTEMCTL}" enable "${APP_NAME}.service"
        "${SYSTEMCTL}" restart "${APP_NAME}.service"
    fi
    echo "updated ${APP_NAME}; existing configuration and data were preserved"
}

uninstall_service() {
    if [[ -z "${INSTALL_ROOT}" ]]; then
        "${SYSTEMCTL}" disable --now "${APP_NAME}.service" 2>/dev/null || true
    fi
    rm -f -- "${BIN_TARGET}" "${SERVICE_TARGET}"
    if [[ -z "${INSTALL_ROOT}" ]]; then
        "${SYSTEMCTL}" daemon-reload
        "${SYSTEMCTL}" reset-failed "${APP_NAME}.service" 2>/dev/null || true
    fi
    echo "uninstalled ${APP_NAME}; preserved ${ENV_TARGET}, ${STATE_DIR}, ${CACHE_DIR}, and the service account"
}

main() {
    require_root
    case "${1:-}" in
        install) install_service ;;
        update) update_service ;;
        uninstall) uninstall_service ;;
        *) usage; exit 2 ;;
    esac
}

main "$@"
