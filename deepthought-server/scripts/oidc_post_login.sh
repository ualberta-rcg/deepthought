#!/bin/bash
# Optional OIDC post-login hook for DeepThought Server.
# Receives `--user <username>` and `OIDC_CLAIM_email` in the environment.

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

EMAIL="${OIDC_CLAIM_email:-}"

if [[ -z "$USERNAME" || -z "$EMAIL" ]]; then
  exit 0
fi

if [[ ! "$USERNAME" =~ ^[a-zA-Z0-9._-]+$ ]]; then
  echo "Invalid username format" >&2
  exit 1
fi
