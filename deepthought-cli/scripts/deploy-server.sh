#!/usr/bin/env bash
# deploy-server.sh — roll the cluster's deepthought-server to a built image tag.
#
# Usage: scripts/deploy-server.sh server-<shortsha> [aleph-host]
#
# Flow (mirrors the 56-edge-routes house pattern): the deployed source of truth
# is /var/lib/rancher/rke2/server/manifests/57-deepthought.yaml on the aleph1
# control-plane; editing the image tag makes RKE2 re-apply it and perform a
# rolling update (maxUnavailable 0). Changes ONLY the deepthought namespace —
# anything else on the cluster is off-limits.
set -euo pipefail

TAG="${1:?usage: deploy-server.sh server-<shortsha> [aleph-host]}"
HOST="${2:-172.26.92.43}"
MANIFEST=/var/lib/rancher/rke2/server/manifests/57-deepthought.yaml

# Sanity: the tag must exist on Docker Hub before we point the cluster at it.
if ! curl -sf "https://hub.docker.com/v2/repositories/rkhoja/deepthought-server/tags/$TAG" >/dev/null; then
    echo "tag rkhoja/deepthought-server:$TAG not found on Docker Hub" >&2
    exit 1
fi

ssh "root@${HOST}" "sed -i.bak -E 's|(image: rkhoja/deepthought-server:).*|\1${TAG}|' ${MANIFEST}"
echo "rolled ${MANIFEST} to ${TAG}; watch: kubectl -n deepthought rollout status deploy/deepthought-server"
