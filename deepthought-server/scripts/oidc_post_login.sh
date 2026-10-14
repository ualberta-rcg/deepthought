#!/bin/bash
# OOD-compatible OIDC post-login hook for DeepThought Server.
# Modeled on Vulcan's /opt/ood/scripts/ood_pun_oidc_email.sh:
# receives `--user <username>` and `OIDC_CLAIM_email` (or `OOD_OIDC_CLAIM_email`)
# in the environment and persists the user's OIDC email to ~/ondemand/oidc_email.txt.

set -euo pipefail

USERNAME=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --user)
      USERNAME="${2:-}"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

EMAIL="${OIDC_CLAIM_email:-${OOD_OIDC_CLAIM_email:-}}"

if [[ -z "$USERNAME" || -z "$EMAIL" ]]; then
  exit 0
fi

if [[ ! "$USERNAME" =~ ^[a-zA-Z0-9._-]+$ ]]; then
  echo "Invalid username format" >&2
  exit 1
fi

USER_HOME=$(getent passwd "$USERNAME" | cut -d: -f6 || true)
if [[ -z "$USER_HOME" || ! -d "$USER_HOME" ]]; then
  exit 0
fi

TARGET_DIR="${USER_HOME}/ondemand"
TARGET_FILE="${TARGET_DIR}/oidc_email.txt"

mkdir -p "$TARGET_DIR"
printf '%s\n' "$EMAIL" > "${TARGET_FILE}.tmp"
chmod 0644 "${TARGET_FILE}.tmp"
mv -f "${TARGET_FILE}.tmp" "$TARGET_FILE"
