#!/usr/bin/env bash
# Piso de cobertura por pacote (#56). Falha se algum pacote com piso ficar abaixo dele.
#
#   scripts/coverage-gate.sh [perfil.out]
#
# Sem argumento, roda os testes (com -tags integration, exige HANKO_TEST_DATABASE_URL) e gera o perfil.
# Com um perfil já pronto (go test -coverprofile=... ./...), só confere. A cobertura é a de declarações
# do próprio pacote: para cada pacote, declarações cobertas ÷ declarações (o mesmo número do "coverage:" do go test).
#
# Pisos (docs/validacao/trilha-ja.md tem a medição que os originou):
#   internal/domain     80   (regra pura: precisa ser quase toda testada)
#   internal/heartbeat  80
#   internal/service    70
#   internal/api        sem piso: coberto por integração (só é relatado)
# Para subir o piso de um pacote sem editar o script: COVERAGE_FLOOR_<PKG>=85 (PKG em maiúsculas: DOMAIN, HEARTBEAT, SERVICE).
set -euo pipefail

cd "$(dirname "$0")/.."

floor_domain=${COVERAGE_FLOOR_DOMAIN:-80}
floor_heartbeat=${COVERAGE_FLOOR_HEARTBEAT:-80}
floor_service=${COVERAGE_FLOOR_SERVICE:-70}

profile=${1:-}
cleanup=
if [ -z "$profile" ]; then
  profile=$(mktemp)
  cleanup=$profile
  trap '[ -z "$cleanup" ] || rm -f "$cleanup"' EXIT
  go test -p 1 -timeout 40m -tags integration -covermode=atomic -coverprofile="$profile" \
    ./internal/domain/... ./internal/heartbeat/... ./internal/service/... ./internal/api/...
fi
[ -s "$profile" ] || { echo "coverage-gate: perfil vazio ou inexistente: $profile" >&2; exit 2; }

module=$(go list -m)

# bloco do perfil: arquivo:inicio,fim nstmts contagem. Deduplica blocos repetidos (perfis mesclados) pelo maior contador.
report=$(awk -v mod="$module" '
  NR == 1 && /^mode:/ { next }
  {
    split($1, a, ":"); file = a[1]; key = $1; n = $2 + 0; c = $3 + 0
    if (!(key in seen) || c > cnt[key]) { cnt[key] = c }
    seen[key] = 1; stm[key] = n; fil[key] = file
  }
  END {
    for (k in seen) {
      f = fil[k]; sub("^" mod "/", "", f); sub("/[^/]*$", "", f)
      tot[f] += stm[k]; if (cnt[k] > 0) cov[f] += stm[k]
    }
    for (p in tot) printf "%s %.1f\n", p, (tot[p] ? 100 * cov[p] / tot[p] : 0)
  }' "$profile" | sort)

status=0
printf '%-28s %9s %7s  %s\n' pacote cobertura piso resultado
while read -r pkg pct; do
  floor=
  case "$pkg" in
    internal/domain) floor=$floor_domain ;;
    internal/heartbeat) floor=$floor_heartbeat ;;
    internal/service) floor=$floor_service ;;
  esac
  case "$pkg" in internal/domain | internal/heartbeat | internal/service | internal/api | internal/api/*) ;; *) continue ;; esac
  if [ -z "$floor" ]; then
    printf '%-28s %8s%% %7s  %s\n' "$pkg" "$pct" "-" "(sem piso: integração)"
    continue
  fi
  if awk -v p="$pct" -v f="$floor" 'BEGIN { exit !(p + 0 >= f + 0) }'; then
    printf '%-28s %8s%% %6s%%  ok\n' "$pkg" "$pct" "$floor"
  else
    printf '%-28s %8s%% %6s%%  ABAIXO DO PISO\n' "$pkg" "$pct" "$floor"
    status=1
  fi
done <<<"$report"

for pkg in internal/domain internal/heartbeat internal/service; do
  grep -q "^$pkg " <<<"$report" || { echo "coverage-gate: pacote $pkg ausente do perfil" >&2; status=1; }
done

[ "$status" -eq 0 ] && echo "coverage-gate: ok" || echo "coverage-gate: FALHOU" >&2
exit "$status"
