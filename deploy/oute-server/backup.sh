#!/usr/bin/env bash
# Backup do Hanko em produção (roda DENTRO do LXC hanko-prd, como root, pelo timer hanko-backup).
# Gera, em HANKO_BACKUP_DIR (padrão /var/backups/hanko, modo 0700), um par consistente:
#   hanko-<UTC>.pgdump         pg_dump -Fc do banco (snapshot transacional; conferido com pg_restore -l)
#   aimemory-<UTC>.tar.gz      volume do ai-memory (wiki git + índice SQLite) com o container pausado
#   SHA256SUMS                 somas dos arquivos retidos
# O backup do lab (restic, semanal) coleta esse diretório (backup.backup_paths no inventário) além
# do próprio pg_dump; aqui fica a cópia local rápida (retém HANKO_BACKUP_KEEP gerações, padrão 3).
# Restore: deploy/oute-server/restore.sh (docs/RUNBOOK.md, seção "Backup e restore do prd").
set -euo pipefail
umask 077

APP_DIR="${HANKO_APP_DIR:-/opt/app}"
OUT="${HANKO_BACKUP_DIR:-/var/backups/hanko}"
KEEP="${HANKO_BACKUP_KEEP:-3}"
PG_USER="${HANKO_PG_USER:-hanko}"
PG_DB="${HANKO_PG_DB:-hanko}"
MEM_SERVICE="${HANKO_MEMORY_SERVICE:-ai-memory}"
MEM_MOUNT="${HANKO_MEMORY_MOUNT:-/data}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"

compose() { docker compose -f "${APP_DIR}/docker-compose.yml" --project-directory "$APP_DIR" "$@"; }
log() { printf '%s backup: %s\n' "$(date -u +%FT%TZ)" "$*"; }

mkdir -p "$OUT"; chmod 700 "$OUT"
tmp="$(mktemp -d "${OUT}/.tmp.XXXXXX")"
cid=""
cleanup() {
  [[ -z "$cid" ]] || docker unpause "$cid" >/dev/null 2>&1 || true   # nunca deixa a memória congelada
  rm -rf "$tmp"
}
trap cleanup EXIT

# 1) Postgres: dump em formato custom (o mesmo que o lab faz; esta cópia é a local/rápida)
log "pg_dump ${PG_DB}"
compose exec -T postgres pg_dump -U "$PG_USER" -Fc --no-owner --no-acl "$PG_DB" > "${tmp}/hanko.pgdump"
[[ -s "${tmp}/hanko.pgdump" ]] || { log "ERRO: dump vazio"; exit 1; }
pg_list="$(compose exec -T postgres pg_restore -l < "${tmp}/hanko.pgdump" | wc -l)"
(( pg_list > 10 )) || { log "ERRO: dump ilegível (pg_restore -l = ${pg_list} linhas)"; exit 1; }

# 2) ai-memory: pausa o container (congela cgroup, sem perder conexões) enquanto o volume é arquivado,
#    senão o índice SQLite pode sair com páginas de momentos diferentes. Sem o profile `memory`, pula.
cid="$(compose ps -q "$MEM_SERVICE" 2>/dev/null || true)"
if [[ -n "$cid" ]]; then
  src="$(docker inspect -f "{{range .Mounts}}{{if eq .Destination \"${MEM_MOUNT}\"}}{{.Source}}{{end}}{{end}}" "$cid")"
  [[ -n "$src" && -d "$src" ]] || { log "ERRO: volume do ${MEM_SERVICE} não encontrado"; exit 1; }
  log "tar do volume ${MEM_SERVICE} (${src}), container pausado"
  docker pause "$cid" >/dev/null
  tar czf "${tmp}/aimemory.tar.gz" -C "$src" .
  docker unpause "$cid" >/dev/null; cid=""
  tar tzf "${tmp}/aimemory.tar.gz" >/dev/null
else
  log "AVISO: ${MEM_SERVICE} não está rodando; sem tar da memória nesta geração"
fi

# 3) Publica atomicamente (mv no mesmo filesystem), poda e recalcula as somas
mv "${tmp}/hanko.pgdump" "${OUT}/hanko-${TS}.pgdump"
[[ ! -f "${tmp}/aimemory.tar.gz" ]] || mv "${tmp}/aimemory.tar.gz" "${OUT}/aimemory-${TS}.tar.gz"
for prefix in hanko aimemory; do
  # sem nenhuma geração do prefixo (ex.: ai-memory fora do ar) o ls falha e, com pipefail, derrubaria o script aqui
  # shellcheck disable=SC2012
  { ls -1 "${OUT}/${prefix}-"*.* 2>/dev/null || true; } | sort -r | tail -n +$((KEEP + 1)) | xargs -r rm -f --
done
# nullglob: padrão sem arquivo some em vez de virar texto literal (senão o sha256sum falha e o mv nunca roda)
( shopt -s nullglob; cd "$OUT" && sha256sum hanko-*.pgdump aimemory-*.tar.gz > SHA256SUMS.new && mv SHA256SUMS.new SHA256SUMS )
log "ok: $(ls -1 "$OUT" | tr '\n' ' ')"
