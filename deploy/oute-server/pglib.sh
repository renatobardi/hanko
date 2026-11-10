# shellcheck shell=bash
# Funções comuns do backup.sh e do restore.sh para falar com o Postgres do Hanko (ADR-0092). Dois modos:
#   local    o banco é o serviço `postgres` do compose (profile localdb); os clientes rodam dentro dele (docker compose exec)
#   externo  HANKO_DATABASE_URL do .env aponta para outro servidor (prd: schema hanko_prd do Postgres compartilhado do lab); os
#            clientes rodam num contêiner descartável da imagem do Postgres (HANKO_PG_CLIENT_IMAGE), com a conexão só
#            em variáveis PG* do ambiente: a senha nunca vai para argv nem para a tela.
# Quem usa define APP_DIR, PG_USER, PG_DB e a função compose() antes de chamar pg_mode_init.

PG_CLIENT_IMAGE="${HANKO_PG_CLIENT_IMAGE:-postgres:17}"
PG_MODE=local
PG_SCHEMA=public

# urldecode: %XX → byte (a senha gerada pode vir codificada na URL)
_pg_urldecode() { local s="${1//+/ }"; printf '%b' "${s//%/\\x}"; }

# pg_mode_init lê a HANKO_DATABASE_URL do .env e decide o modo. Sem URL ou com host `postgres`: local.
pg_mode_init() {
  local url re q
  url=""
  # sem .env (ou sem a linha) = local; com pipefail, o sed num arquivo ausente derrubaria o script
  [[ ! -f "${APP_DIR}/.env" ]] || url="$({ sed -n 's/^HANKO_DATABASE_URL=//p' "${APP_DIR}/.env" || true; } | tail -1)"
  url="${url%\"}"; url="${url#\"}"
  re='^postgres(ql)?://([^:@/]+)(:([^@]*))?@([^:/?]+)(:([0-9]+))?/([^?]+)(\?(.*))?$'
  if [[ -z "$url" ]]; then return 0; fi
  [[ "$url" =~ $re ]] || { echo "erro: HANKO_DATABASE_URL do .env fora do formato postgres://usuario:senha@host:porta/banco" >&2; return 1; }
  [[ "${BASH_REMATCH[5]}" != postgres ]] || return 0
  PG_MODE=external
  export PGUSER PGPASSWORD PGHOST PGPORT PGDATABASE PGSSLMODE
  PGUSER="$(_pg_urldecode "${BASH_REMATCH[2]}")"
  PGPASSWORD="$(_pg_urldecode "${BASH_REMATCH[4]}")"
  PGHOST="${BASH_REMATCH[5]}"
  PGPORT="${BASH_REMATCH[7]:-5432}"
  PGDATABASE="$(_pg_urldecode "${BASH_REMATCH[8]}")"
  q="${BASH_REMATCH[10]}"
  PGSSLMODE=prefer
  if [[ "&${q}&" =~ \&sslmode=([a-z-]+)\& ]]; then PGSSLMODE="${BASH_REMATCH[1]}"; fi
  PG_SCHEMA="$(pgc psql -X -A -t -v ON_ERROR_STOP=1 -c 'SELECT current_schema()' </dev/null | tr -d '[:space:]')" \
    || { echo "erro: não consegui conectar ao banco externo (${PGHOST}:${PGPORT})" >&2; return 1; }
  [[ "$PG_SCHEMA" =~ ^[a-z_][a-z0-9_]{0,62}$ ]] || { echo "erro: schema corrente inválido: '${PG_SCHEMA}'" >&2; return 1; }
}

# pgc CMD ARGS… roda um cliente do Postgres conectado ao banco do Hanko. Stdin passa adiante. PG_MOUNT (opcional) é um
# diretório do LXC montado em /w no contêiner descartável (o modo local vê o mesmo caminho pelo stdin, não por /w).
pgc() {
  if [[ "$PG_MODE" == local ]]; then
    local cmd="$1"; shift
    case "$cmd" in
      psql|pg_dump|pg_restore) compose exec -T postgres "$cmd" -U "$PG_USER" "$@" ;;
      *) compose exec -T postgres "$cmd" "$@" ;;
    esac
  else
    local mount=()
    [[ -z "${PG_MOUNT:-}" ]] || mount=(-v "${PG_MOUNT}:/w")
    docker run --rm -i "${mount[@]}" -e PGUSER -e PGPASSWORD -e PGHOST -e PGPORT -e PGDATABASE -e PGSSLMODE \
      -e PGCONNECT_TIMEOUT=15 "$PG_CLIENT_IMAGE" "$@"
  fi
}

# pg_dump_tables_outside SCHEMA < dump: imprime as tabelas do dump fora do schema (vazio = dump só do schema).
pg_dump_tables_outside() {
  awk -v s="$1" '$1 !~ /^;/ && $4 == "TABLE" && $5 != "DATA" && $5 != s { print $5 "." $6 }'
}

# pg_clear_schema_sql SCHEMA: SQL que esvazia o schema sem apagá-lo (o mesmo texto do restore interno; teste em
# internal/deploy confere que os dois são iguais).
pg_clear_schema_sql() { sed "s/__SCHEMA__/$1/g" "$(dirname "${BASH_SOURCE[0]}")/clear-schema.sql"; }
