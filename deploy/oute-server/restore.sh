#!/usr/bin/env bash
# Restore do Hanko em produção (roda DENTRO do LXC hanko-prd, como root). Contraparte de backup.sh.
#
#   restore.sh --check [--pgdump ARQ]            drill: restaura num banco descartável e confere; NÃO toca no prd
#   restore.sh --apply [--pgdump ARQ] [--aimemory ARQ] [--blobs ARQ] [--yes]
#                                                restaura o dump num banco novo `hanko` (o atual vira hanko_pre_restore_<ts>,
#                                                nunca é dropado), substitui o volume do ai-memory e repõe os artefatos em disco
# Sem --pgdump/--aimemory/--blobs usa os arquivos mais novos de HANKO_BACKUP_DIR (/var/backups/hanko).
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
BLOB_MOUNT="${HANKO_BLOB_MOUNT:-/var/lib/hanko/blobs}"
MODE=""; PGDUMP=""; AIMEM=""; BLOBS=""; YES=false
usage() { sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }
while [[ $# -gt 0 ]]; do case "$1" in
  --check) MODE=check ;; --apply) MODE=apply ;; --yes) YES=true ;;
  --pgdump) PGDUMP="${2:?}"; shift ;; --aimemory) AIMEM="${2:?}"; shift ;; --blobs) BLOBS="${2:?}"; shift ;;
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
if [[ -z "$BLOBS" ]]; then BLOBS="$(newest blobs)"; fi
log "dump: ${PGDUMP}; memória: ${AIMEM:-<nenhuma>}; artefatos: ${BLOBS:-<nenhum>}"
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
  if [[ -n "$BLOBS" ]]; then tar tf "$BLOBS" >/dev/null && log "tar dos artefatos legível"; fi
  log "drill ok (prd intocado)"
  exit 0
fi

if ! $YES; then
  read -r -p "Substituir o banco ${PG_DB} e o volume do ${MEM_SERVICE} deste LXC? digite 'sim': " ans
  [[ "$ans" == sim ]] || { echo "abortado"; exit 1; }
fi
# O banco atual NÃO é dropado: é renomeado para ${PG_DB}_pre_restore_<ts> e o dump vai para um banco novo
# ${PG_DB}. Se o restore falhar, o trap devolve o nome e sobe a stack. O descarte do antigo é manual,
# só depois do readyz.
TS="$(date -u +%Y%m%d%H%M%S)"
OLD_DB="${PG_DB}_pre_restore_${TS}"
READYZ_URL="${HANKO_READYZ_URL:-http://127.0.0.1:${HANKO_PORT:-3810}/readyz}"
READYZ_WAIT="${HANKO_READYZ_WAIT:-90}"
renamed=false; swapped=false
rollback() {
  local rc=$?
  trap - EXIT
  [[ $rc -eq 0 ]] && return 0
  if ! $swapped; then
    log "FALHA (código ${rc}): voltando o banco anterior e subindo a stack"
    psql_ -d postgres -c "DROP DATABASE IF EXISTS ${PG_DB} WITH (FORCE)" || log "ERRO: não consegui descartar o restore parcial em ${PG_DB}"
    if $renamed; then
      psql_ -d postgres -c "ALTER DATABASE ${OLD_DB} RENAME TO ${PG_DB}" \
        || log "ERRO: não consegui devolver ${OLD_DB} para ${PG_DB}; o dado antigo está em ${OLD_DB}"
    fi
    compose up -d || log "ERRO: não consegui subir a stack; rode 'docker compose up -d' em ${APP_DIR}"
  else
    log "FALHA (código ${rc}) depois de restaurar o banco: a stack NÃO foi subida; banco anterior em ${OLD_DB}"
  fi
  exit "$rc"
}
trap rollback EXIT

log "parando hanko e ${MEM_SERVICE}"
compose stop hanko "$MEM_SERVICE" >/dev/null 2>&1 || compose stop hanko
if [[ "$(psql_ -d postgres -At -c "select 1 from pg_database where datname='${PG_DB}'")" == 1 ]]; then
  psql_ -d postgres -c "select pg_terminate_backend(pid) from pg_stat_activity where datname='${PG_DB}' and pid <> pg_backend_pid()" >/dev/null
  psql_ -d postgres -c "ALTER DATABASE ${PG_DB} RENAME TO ${OLD_DB}"
  renamed=true
  log "banco atual guardado como ${OLD_DB}"
fi
psql_ -d postgres -c "CREATE DATABASE ${PG_DB}"
log "pg_restore em ${PG_DB}"
compose exec -T postgres pg_restore -U "$PG_USER" -d "$PG_DB" --no-owner --no-acl --exit-on-error < "$PGDUMP"
swapped=true
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
# Artefatos (driver fs, ADR-0065): extrai por cima do volume, sem apagar. Cada arquivo é uma versão imutável com nome
# único, então o que o backup tem volta igual, e um arquivo mais novo que o dump só fica sem referência.
if [[ -n "$BLOBS" ]]; then
  hid="$(compose ps -aq hanko 2>/dev/null || true)"
  bsrc=""
  [[ -z "$hid" ]] || bsrc="$(docker inspect -f "{{range .Mounts}}{{if eq .Destination \"${BLOB_MOUNT}\"}}{{.Source}}{{end}}{{end}}" "$hid")"
  if [[ -n "$bsrc" && -d "$bsrc" ]]; then
    log "restaurando artefatos em ${bsrc}"
    tar xf "$BLOBS" -C "$bsrc"
  else
    log "AVISO: volume de artefatos (${BLOB_MOUNT}) não encontrado; tar dos artefatos NÃO restaurado"
  fi
fi
log "subindo a stack"
compose up -d
ready=false
for ((i = 0; i < READYZ_WAIT; i += 2)); do
  if curl -fsS --max-time 2 "$READYZ_URL" >/dev/null 2>&1; then ready=true; break; fi
  sleep 2
done
if $ready; then
  log "readyz ok"
  if $renamed; then
    log "banco anterior preservado como ${OLD_DB}. Quando confirmar o restore, descarte-o com:"
    log "  docker compose -f ${APP_DIR}/docker-compose.yml exec -T postgres psql -U ${PG_USER} -d postgres -c 'DROP DATABASE ${OLD_DB}'"
  fi
else
  log "AVISO: readyz não respondeu em ${READYZ_WAIT}s (${READYZ_URL}); confira os logs. Banco anterior preservado como ${OLD_DB:-<nenhum>}; NÃO o descarte"
  exit 1
fi
