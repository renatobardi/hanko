# shellcheck shell=bash
# Funções comuns do backup.sh e do restore.sh (ADR-0092, ADR-0093). Dois modos, pela HANKO_DATABASE_URL do .env:
#   local    sem URL ou com host `postgres`: o banco é o serviço `postgres` do compose (profile localdb); o dump e o
#            restore do banco rodam dentro dele (docker compose exec)
#   externo  a URL aponta para outro servidor (prd: schema hanko_prd do Postgres compartilhado do lab). O banco não é
#            deste LXC: o dump dele é o do backup do banco compartilhado, e aqui ficam só memória e artefatos.
# Quem usa define APP_DIR, PG_USER e a função compose() antes de chamar pg_mode_init.

# shellcheck disable=SC2034  # lido pelo backup.sh e pelo restore.sh
PG_MODE=local

# pg_mode_init lê a HANKO_DATABASE_URL do .env e decide o modo. Não conecta nem lê a senha.
pg_mode_init() {
  local url=""
  # sem .env (ou sem a linha) = local; com pipefail, o sed num arquivo ausente derrubaria o script
  [[ ! -f "${APP_DIR}/.env" ]] || url="$({ sed -n 's/^HANKO_DATABASE_URL=//p' "${APP_DIR}/.env" || true; } | tail -1)"
  url="${url%\"}"; url="${url#\"}"
  case "$url" in
    ''|*@postgres:*|*@postgres/*) PG_MODE=local ;;
    *) PG_MODE=external ;;
  esac
}

# pgc CMD ARGS… roda um cliente do Postgres no serviço `postgres` do compose (só no modo local). Stdin passa adiante.
pgc() {
  local cmd="$1"; shift
  case "$cmd" in
    psql|pg_dump|pg_restore) compose exec -T postgres "$cmd" -U "$PG_USER" "$@" ;;
    *) compose exec -T postgres "$cmd" "$@" ;;
  esac
}
