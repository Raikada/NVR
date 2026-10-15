#!/bin/sh
# One-command NVR update for the NAS: pull the newest published image,
# restart onto it, prune the old one. State (cameras, users, footage)
# lives on the /data and /recordings mounts and survives untouched;
# schema migrations apply automatically at boot.
set -eu
cd "$(dirname "$0")"
COMPOSE="docker compose -f docker-compose.nas.yml"

echo "==> pulling newest image"
sudo $COMPOSE pull
echo "==> restarting onto it"
sudo $COMPOSE up -d
echo "==> pruning superseded images"
sudo docker image prune -f >/dev/null
echo "==> running version:"
sleep 3
curl -sk https://localhost:9997/v1/system/info || true
echo
