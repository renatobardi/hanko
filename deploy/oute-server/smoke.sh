#!/usr/bin/env bash
# Smoke de produção: roda DENTRO do LXC hanko-prd (o deploy-prd.sh chama no fim) ou em qualquer
# máquina com acesso ao Hanko. Só leitura: não cria nada, não precisa de token.
#
#   BASE=http://127.0.0.1:3810 PUBLIC_HOST=hanko.oute.pro deploy/oute-server/smoke.sh
#
# Sai com 1 no primeiro item que falhar e imprime o que esperava × o que veio.
set -uo pipefail

BASE="${BASE:-http://127.0.0.1:3810}"
PUBLIC_HOST="${PUBLIC_HOST:-hanko.oute.pro}"
COMPOSE="${COMPOSE:-/opt/app/docker-compose.yml}"
fail=0

check() { # nome, status esperado, curl args...
  local name="$1" want="$2"; shift 2
  local got
  got="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$@")"
  if [[ "$got" == "$want" ]]; then
    printf '  ok    %-44s %s\n' "$name" "$got"
  else
    printf '  FALHA %-44s esperado %s, veio %s\n' "$name" "$want" "$got"
    fail=1
  fi
}

echo "==> smoke ${BASE} (Host: ${PUBLIC_HOST})"
check "healthz"                          200 -H "Host: ${PUBLIC_HOST}" "${BASE}/healthz"
check "readyz (Postgres + migrations)"   200 -H "Host: ${PUBLIC_HOST}" "${BASE}/readyz"
check "metrics"                          200 -H "Host: ${PUBLIC_HOST}" "${BASE}/metrics"
check "tela de login"                    200 -H "Host: ${PUBLIC_HOST}" "${BASE}/entrar"
check "UI sem sessão redireciona"        303 -H "Host: ${PUBLIC_HOST}" "${BASE}/"
check "API sem token é recusada"         401 -H "Host: ${PUBLIC_HOST}" "${BASE}/api/v1/companies"
check "Host desconhecido é recusado"     421 -H "Host: rebinding.example" "${BASE}/healthz"

if command -v docker >/dev/null 2>&1 && [[ -f "$COMPOSE" ]]; then
  cid="$(docker compose -f "$COMPOSE" ps -q ai-memory 2>/dev/null)"
  if [[ -n "$cid" ]]; then
    st="$(docker inspect -f '{{.State.Health.Status}}' "$cid" 2>/dev/null)"
    if [[ "$st" == healthy ]]; then printf '  ok    %-44s %s\n' "ai-memory" "$st"
    else printf '  FALHA %-44s %s\n' "ai-memory" "${st:-sem status}"; fail=1; fi
  else
    printf '  aviso %-44s %s\n' "ai-memory" "não está rodando (profile memory desligado?)"
  fi
fi

if (( fail )); then echo "==> smoke FALHOU"; exit 1; fi
echo "==> smoke ok"
