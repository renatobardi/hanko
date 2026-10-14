#!/usr/bin/env bash
# Provisiona um container LXD para o Hanko (hanko-stg | hanko-prd). Roda NO oute-server.
# Uso: deploy/lxc/setup.sh hanko-stg
set -euo pipefail
name="${1:?uso: setup.sh <hanko-stg|hanko-prd>}"

lxc launch ubuntu:24.04 "$name" -c security.nesting=true -c security.syscalls.intercept.mknod=true
lxc exec "$name" -- bash -euxc '
  apt-get update && apt-get install -y ca-certificates curl git restic postgresql-client
  curl -fsSL https://get.docker.com | sh
  useradd -m -s /bin/bash hanko && usermod -aG docker hanko
  install -d -o hanko -g hanko /opt/hanko
'
echo "Container $name pronto. Próximo passo: docs/RUNBOOK.md, seção Deploy LXC (copiar deploy/ e .env)."
