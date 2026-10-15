#!/usr/bin/env bash
# Restore do Hanko em produção (roda DENTRO do LXC hanko-prd, como root). Contraparte de backup.sh.
#
#   restore.sh --check [--pgdump ARQ]            drill: restaura num banco descartável e confere; NÃO toca no prd
#   restore.sh --apply [--pgdump ARQ] [--aimemory ARQ] [--yes]
#                                                substitui o banco `hanko` e o volume do ai-memory
# Sem --pgdump/--aimemory usa os arquivos mais novos de HANKO_BACKUP_DIR (/var/backups/hanko).
# Para trazer de fora (backup do lab), veja docs/RUNBOOK.md: extraia o arquivo e passe o caminho aqui.
# --apply para o hanko e o ai-memory, e sobe de novo ao final. Migrations são append-only: restaurar um
# dump mais velho que o binário em execução é seguro (o boot migra para frente).
set -euo pipefail
umask 077

APP_DIR="${HANKO_APP_DIR:-/opt/app}"
DIR="${HANKO_BACKUP_DIR:-/var/backups/hanko}"
PG_USER="${HANKO_PG_USER:-hanko}"
PG_DB="${HANKO_PG_DB:-hanko}"
MEM_SERVICE="${HANKO_MEMORY_SERVICE:-ai-memory}"
MEM_MOUNT="${HANKO_MEMORY_MOUNT:-/data}"
MODE=""; PGDUMP=""; AIMEM=""; YES=false
usage() { sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }
while [[ $# -gt 0 ]]; do case "$1" in
  --check) MODE=check ;; --apply) MODE=apply ;; --yes) YES=true ;;
  --pgdump) PGDUMP="${2:?}"; shift ;; --aimemory) AIMEM="${2:?}"; shift ;;
  *) usage ;;
esac; shift; done
[[ -n "$MODE" ]] || usage

compose() { docker compose -f "${APP_DIR}/docker-compose.yml" --project-directory "$APP_DIR" "$@"; }
log() { printf '%s restore: %s\n' "$(date -u +%FT%TZ)" "$*"; }
newest() { ls -1 "${DIR}/$1"-* 2>/dev/null | sort -r | head -1 || true; }
psql_() { compose exec -T postgres psql -U "$PG_USER" -v ON_ERROR_STOP=1 -X -q "$@"; }

[[ -n "$PGDUMP" ]] || PGDUMP="$(newest hanko)"
[[ -f "$PGDUMP" ]] || { echo "erro: sem dump do Postgres (use --pgdump ARQ)" >&2; exit 1; }
if [[ -z "$AIMEM" ]]; then AIMEM="$(newest aimemory)"; fi
log "dump: ${PGDUMP}; memória: ${AIMEM:-<nenhuma>}"
compose exec -T postgres pg_restore -l < "$PGDUMP" >/dev/null || { echo "erro: dump ilegível" >&2; exit 1; }

if [[ "$MODE" == check ]]; then
  scratch="${PG_DB}_restore_check"
  log "drill no banco descartável ${scratch}"
  psql_ -d postgres -c "DROP DATABASE IF EXISTS ${scratch} WITH (FORCE)" -c "CREATE DATABASE ${scratch}"
  trap 'psql_ -d postgres -c "DROP DATABASE IF EXISTS '"${scratch}"' WITH (FORCE)" >/dev/null 2>&1 || true' EXIT
  compose exec -T postgres pg_restore -U "$PG_USER" -d "$scratch" --no-owner --no-acl --exit-on-error < "$PGDUMP"
  psql_ -d "$scratch" -At -c "select 'tabelas='||count(*) from information_schema.tables where table_schema='public'" \
                           -c "select 'migration='||max(version_id) from goose_db_version where is_applied"
  if [[ -n "$AIMEM" ]]; then tar tzf "$AIMEM" >/dev/null && log "tar da memória legível"; fi
  log "drill ok (prd intocado)"
  exit 0
fi

if ! $YES; then
  read -r -p "Substituir o banco ${PG_DB} e o volume do ${MEM_SERVICE} deste LXC? digite 'sim': " ans
  [[ "$ans" == sim ]] || { echo "abortado"; exit 1; }
fi
log "parando hanko e ${MEM_SERVICE}"
compose stop hanko "$MEM_SERVICE" >/dev/null 2>&1 || compose stop hanko
psql_ -d postgres -c "DROP DATABASE IF EXISTS ${PG_DB} WITH (FORCE)" -c "CREATE DATABASE ${PG_DB}"
log "pg_restore em ${PG_DB}"
compose exec -T postgres pg_restore -U "$PG_USER" -d "$PG_DB" --no-owner --no-acl --exit-on-error < "$PGDUMP"
if [[ -n "$AIMEM" ]]; then
  cid="$(compose ps -aq "$MEM_SERVICE" 2>/dev/null || true)"
  src=""
  [[ -z "$cid" ]] || src="$(docker inspect -f "{{range .Mounts}}{{if eq .Destination \"${MEM_MOUNT}\"}}{{.Source}}{{end}}{{end}}" "$cid")"
  if [[ -n "$src" && -d "$src" ]]; then
    log "restaurando volume do ${MEM_SERVICE} em ${src}"
    find "$src" -mindepth 1 -delete
    tar xzf "$AIMEM" -C "$src"
  else
    log "AVISO: volume do ${MEM_SERVICE} não encontrado; tar da memória NÃO restaurado"
  fi
fi
log "subindo a stack"
compose up -d
log "feito; confira: curl -fsS http://127.0.0.1:\${HANKO_PORT:-3810}/readyz"
