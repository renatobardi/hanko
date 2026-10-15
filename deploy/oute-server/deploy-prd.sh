#!/usr/bin/env bash
# Deploy do Hanko em produção no oute-server, seguindo as convenções do repo lab
# (docs/oute-server-app-setup.md): LXC Ubuntu 24.04 não privilegiado com nesting, IP estático,
# app em /opt/app com docker compose, borda pública por nginx + certbot no host.
#
# Roda NO MAC, na raiz do repo Hanko:  deploy/oute-server/deploy-prd.sh [--edge] [--dry-run]
#   (sem flags)  provisiona/atualiza o LXC hanko-prd e sobe a stack; valida o /healthz.
#   --edge       também emite o certificado e instala o vhost nginx público de hanko.oute.pro no host
#                (sudo no oute-server; senha do Keychain `lab-oute-sudo`, como nos scripts do lab).
#   --dry-run    só imprime os comandos.
# Requisitos: `ssh oute-server` funcionando (usuário ubuntu, grupo lxd), working tree limpo.
set -euo pipefail

HOST="${OUTE_HOST:-oute-server}"
NAME="${HANKO_LXC:-hanko-prd}"
IP="${HANKO_LXC_IP:-}"
PORT="${HANKO_PORT:-3810}"
GATEWAY="${LXC_GATEWAY:-10.173.117.1}"
DOMAIN="${HANKO_DOMAIN:-hanko.oute.pro}"
# IP Tailscale do host: o split DNS da tailnet resolve *.oute.pro para ele, e os vhosts do lab
# escutam nele explicitamente — sem este listen o nginx não acha o SNI ("unrecognized name").
TS_IP="${OUTE_TAILSCALE_IP:-100.66.254.24}"
EDGE=false; DRY=false
for a in "$@"; do case "$a" in --edge) EDGE=true ;; --dry-run) DRY=true ;; *) echo "uso: $0 [--edge] [--dry-run]" >&2; exit 2 ;; esac; done

cd "$(git rev-parse --show-toplevel)"
[[ -z "$(git status --porcelain)" ]] || { echo "erro: working tree com mudanças; commite antes do deploy" >&2; exit 1; }
REV="$(git rev-parse --short HEAD)"

r() { # executa no oute-server
  if $DRY; then printf 'ssh %s %q\n' "$HOST" "$*"; else ssh -n "$HOST" "$*"; fi
}
x() { # executa dentro do LXC
  r "lxc exec ${NAME} -- bash -lc $(printf '%q' "$*")"
}

echo "==> Hanko ${REV} → ${HOST}:${NAME} (porta ${PORT}, https://${DOMAIN})"

# 0) IP: o PORTS.md do lab pode estar defasado; escolhe o primeiro livre olhando o LXD de verdade
if [[ -z "$IP" ]]; then
  if ssh -n "$HOST" "lxc info ${NAME}" >/dev/null 2>&1 && \
     cur="$(ssh -n "$HOST" "lxc config device get ${NAME} eth0 ipv4.address" 2>/dev/null)" && [[ -n "$cur" ]]; then
    IP="$cur"
  else
    IP="$(ssh -n "$HOST" "lxc list --format json" | python3 -c '
import json, sys
used = set()
for c in json.load(sys.stdin):
    eth0 = (c.get("expanded_devices") or {}).get("eth0") or {}
    if eth0.get("ipv4.address"): used.add(eth0["ipv4.address"])
    for net in ((c.get("state") or {}).get("network") or {}).values():
        for a in net.get("addresses", []):
            if a.get("family") == "inet": used.add(a["address"])
for i in range(41, 250):
    ip = f"10.173.117.{i}"
    if ip not in used:
        print(ip); break
')"
  fi
  [[ -n "$IP" ]] || { echo "erro: não achei IP livre em 10.173.117.0/24" >&2; exit 1; }
fi
echo "==> IP do LXC: ${IP}"

# 1) LXC (idempotente: cria se não existe; completa o provisionamento se ficou pela metade)
if ! $DRY && ! ssh -n "$HOST" "lxc info ${NAME}" >/dev/null 2>&1; then
  r "lxc launch ubuntu:24.04 ${NAME} --config security.nesting=true"
fi
$DRY && r "lxc launch ubuntu:24.04 ${NAME} --config security.nesting=true"
if $DRY || [[ "$(ssh -n "$HOST" "lxc config device get ${NAME} eth0 ipv4.address" 2>/dev/null || true)" != "$IP" ]]; then
  r "lxc config device override ${NAME} eth0 ipv4.address=${IP} 2>/dev/null || lxc config device set ${NAME} eth0 ipv4.address=${IP}"
  x "printf '%s\n' 'network: {config: disabled}' > /etc/cloud/cloud.cfg.d/99-disable-network-config.cfg && printf '%s\n' 'network:' '  version: 2' '  ethernets:' '    eth0:' '      addresses:' '        - ${IP}/24' '      routes:' '        - to: default' '          via: ${GATEWAY}' '      nameservers:' '        addresses: [8.8.8.8, 1.1.1.1]' > /etc/netplan/99-oute-static.yaml && chmod 600 /etc/netplan/99-oute-static.yaml"
  r "lxc restart ${NAME}"
  r "lxc exec ${NAME} -- cloud-init status --wait"
fi
if $DRY || ! ssh -n "$HOST" "lxc exec ${NAME} -- sh -c 'command -v docker >/dev/null && docker compose version >/dev/null'" >/dev/null 2>&1; then
  x "for i in \$(seq 1 30); do getent hosts archive.ubuntu.com >/dev/null && break; sleep 2; done; apt-get update && apt-get install -y ca-certificates curl git openssl docker.io docker-compose-v2"
  r "lxc config set ${NAME} boot.autostart true"
fi
r "lxc config set ${NAME} limits.memory ${HANKO_LXC_MEMORY:-4GiB}"  # teto declarado no inventário do lab

# 2) Código: bundle git (o repo é privado; nada de credencial dentro do LXC)
BUNDLE="$(mktemp -t hanko-XXXXXX).bundle"
git bundle create "$BUNDLE" HEAD main >/dev/null 2>&1 || git bundle create "$BUNDLE" HEAD
if $DRY; then echo "scp $BUNDLE ${HOST}:/tmp/hanko.bundle"; else scp -q "$BUNDLE" "${HOST}:/tmp/hanko.bundle"; fi
rm -f "$BUNDLE"
r "lxc file push /tmp/hanko.bundle ${NAME}/root/hanko.bundle && rm -f /tmp/hanko.bundle"
x "set -e; if [ ! -d /opt/app/.git ]; then git clone -q /root/hanko.bundle /opt/app; else cd /opt/app && git fetch -q /root/hanko.bundle HEAD && git reset -q --hard FETCH_HEAD; fi; rm -f /root/hanko.bundle; cd /opt/app && git log --oneline -1"

# 3) .env gerado DENTRO do LXC na primeira vez (segredos nunca saem de lá); config pública sempre atualizada
x "set -e; cd /opt/app; umask 077
if [ ! -f .env ]; then
  printf 'POSTGRES_PASSWORD=%s\nHANKO_SECRET=%s\nAI_MEMORY_AUTH_TOKEN=%s\n' \"\$(openssl rand -hex 24)\" \"\$(openssl rand -hex 32)\" \"\$(openssl rand -hex 24)\" > .env.secrets
  cp .env.secrets .env
fi
grep -v -E '^(HANKO_(MODE|PUBLIC_URL|ALLOWED_HOSTS|TRUSTED_PROXIES|AUTH_PROVIDER|AUTH_OWNER_EMAIL|FIREBASE_PROJECT_ID|FIREBASE_API_KEY|FIREBASE_AUTH_DOMAIN|MEMORY_PROVIDER|VERSION|PORT|LOG_FORMAT))=' .env > .env.tmp || true
cat >> .env.tmp <<'CFG'
HANKO_MODE=authenticated
HANKO_PUBLIC_URL=https://${DOMAIN}
HANKO_ALLOWED_HOSTS=${DOMAIN},${IP}
HANKO_TRUSTED_PROXIES=${GATEWAY}/32,172.16.0.0/12
HANKO_AUTH_PROVIDER=firebase
HANKO_AUTH_OWNER_EMAIL=renatobardicabral@gmail.com
HANKO_FIREBASE_PROJECT_ID=oute-sso
HANKO_FIREBASE_API_KEY=AIzaSyCh2NEff6X4ylSY1jtiFvuNe3fESMKZ5VE
HANKO_FIREBASE_AUTH_DOMAIN=oute-sso.firebaseapp.com
HANKO_MEMORY_PROVIDER=ai-memory
HANKO_VERSION=${REV}
HANKO_PORT=${PORT}
HANKO_LOG_FORMAT=json
CFG
mv .env.tmp .env; chmod 600 .env; rm -f .env.secrets"

# 4) Sobe e valida
x "cd /opt/app && docker compose up -d --build"
x "for i in \$(seq 1 90); do curl -fsS --max-time 2 http://127.0.0.1:${PORT}/readyz >/dev/null && echo 'readyz ok' && exit 0; sleep 2; done; docker compose -f /opt/app/docker-compose.yml logs --tail 80 hanko; exit 1"
r "lxc snapshot ${NAME} deploy-\$(date +%Y%m%d%H%M%S)"

# 5) Borda pública (host): mesmo padrão de lab/scripts/ops/deploy-vault-vhost.sh —
#    certbot --webroot pelo catch-all ACME da porta 80, vhost 443 público, senha sudo do Keychain (lab-oute-sudo).
if $EDGE; then
  if command -v security >/dev/null 2>&1 && security find-generic-password -s lab-oute-sudo -w >/dev/null 2>&1; then
    SUDO_PASS="$(security find-generic-password -s lab-oute-sudo -w)"
  else
    read -r -s -p "Senha sudo de ${HOST}: " SUDO_PASS; echo >&2
  fi
  REMOTE_TMP=/tmp/deploy-hanko-vhost.remote.sh
  ssh "$HOST" "cat > ${REMOTE_TMP}" <<'REMOTE'
set -euo pipefail
DOMAIN=${DOMAIN:?}; UPSTREAM=${UPSTREAM:?}; TS_IP=${TS_IP:?}
AVAILABLE="/etc/nginx/sites-available/${DOMAIN}"; ENABLED="/etc/nginx/sites-enabled/${DOMAIN}"
if [ -d "/etc/letsencrypt/live/${DOMAIN}" ]; then
  echo "==> certificado já existe, reaproveitando"
else
  echo "==> pedindo certificado via HTTP-01"
  mkdir -p /var/www/acme
  certbot certonly --webroot -w /var/www/acme -d "${DOMAIN}" --non-interactive --agree-tos --register-unsafely-without-email
fi
[ ! -f "$AVAILABLE" ] || cp "$AVAILABLE" "${AVAILABLE}.bak.$(date +%Y%m%d-%H%M%S)"
cat > "$AVAILABLE" <<VHOST
server {
    server_name ${DOMAIN};
    client_max_body_size 20m;

    location / {
        proxy_pass http://${UPSTREAM};
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-Host \$host;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        # SSE (log ao vivo das execuções)
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600;
        proxy_send_timeout 3600;
    }

    listen 443 ssl;
    listen [::]:443 ssl;
    listen ${TS_IP}:443 ssl;
    ssl_certificate /etc/letsencrypt/live/${DOMAIN}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/${DOMAIN}/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;
}
VHOST
ln -sfn "$AVAILABLE" "$ENABLED"
nginx -t
systemctl reload nginx
echo "==> nginx recarregado"
REMOTE
  if $DRY; then
    echo "(dry-run) remoto: sudo DOMAIN=${DOMAIN} UPSTREAM=${IP}:${PORT} TS_IP=${TS_IP} bash ${REMOTE_TMP}"
    ssh "$HOST" "rm -f ${REMOTE_TMP}"
  else
    if printf '%s\n' "${SUDO_PASS}" | ssh "$HOST" "sudo -S -p '' DOMAIN='${DOMAIN}' UPSTREAM='${IP}:${PORT}' TS_IP='${TS_IP}' bash ${REMOTE_TMP}"; then
      ssh "$HOST" "rm -f ${REMOTE_TMP}"
    else
      rc=$?; ssh "$HOST" "rm -f ${REMOTE_TMP}" || true; echo "FAIL: borda saiu ${rc}" >&2; exit "$rc"
    fi
    sleep 3  # reload do nginx é assíncrono
    echo "==> https://${DOMAIN}/healthz: $(curl -fsS --max-time 10 "https://${DOMAIN}/healthz" || echo FALHOU)"
  fi
fi

cat <<EOT
==> Pronto: ${NAME} (${IP}:${PORT}) rodando Hanko ${REV}.
Próximos passos:
  - Primeira vez sem --edge? rode de novo com --edge para nginx + certificado de ${DOMAIN}.
  - Inventário do lab: hanko-prd (${IP}:${PORT}, limits.memory 4GiB) e o vhost ${DOMAIN} estão em
    servers/oute-server/inventory.yaml do repo lab; mudou IP, porta ou vhost? atualize lá também.
  - Login do Claude para as personas (uma vez):  ssh -t ${HOST} lxc exec ${NAME} -- docker compose -f /opt/app/docker-compose.yml exec hanko claude
EOT
