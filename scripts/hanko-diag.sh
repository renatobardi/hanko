#!/usr/bin/env bash
# hanko-diag.sh — coleta o que preciso para diagnosticar "erros no log" e "falta de acesso".
# Roda na máquina onde o Hanko está (host ou com docker compose). Não altera nada.
# Segredos (SECRET, TOKEN, KEY, PASSWORD, DATABASE_URL) saem mascarados.
#
# Uso:
#   bash hanko-diag.sh                      # autodetecta binário no host ou compose
#   HANKO_COMPOSE_DIR=/srv/hanko bash hanko-diag.sh   # pasta do docker-compose.yml / compose.yaml
#   HANKO_COMPANY=<slug> HANKO_URL=http://127.0.0.1:7420 HANKO_TOKEN=hk_... bash hanko-diag.sh
#
# Saída: hanko-diag-<data>.txt no diretório atual. Me manda esse arquivo.

set -u
OUT="hanko-diag-$(date +%Y%m%d-%H%M%S).txt"
LOG_LINES="${LOG_LINES:-400}"

exec > >(tee "$OUT") 2>&1

sec() { printf '\n\n===== %s =====\n' "$*"; }
have() { command -v "$1" >/dev/null 2>&1; }
# jfield CHAVE: valores de uma chave string do JSON do CLI (indentado, com espaço depois dos dois-pontos)
jfield() { grep -oE "\"$1\": *\"[^\"]*\"" | sed -E 's/^[^:]*: *"//; s/"$//'; }
uuids() { grep -oE '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}'; }
mask() { sed -E 's/((SECRET|TOKEN|KEY|PASSWORD|DATABASE_URL|API_KEY)[A-Z_]*=)([^ "'"'"']{0,4})[^ "'"'"']*/\1\3****/gI'; }

# ---------- 0. detecção ----------
sec "0. Ambiente"
date; uname -a; id
echo "shell: $SHELL   user HOME: $HOME"

COMPOSE_DIR="${HANKO_COMPOSE_DIR:-}"
if [ -z "$COMPOSE_DIR" ]; then
  for d in . /srv/hanko /opt/hanko "$HOME/hanko" "$HOME/oute/hanko"; do
    if [ -f "$d/docker-compose.yml" ] || [ -f "$d/compose.yaml" ] || [ -f "$d/deploy/compose.yaml" ]; then COMPOSE_DIR="$d"; break; fi
  done
fi
COMPOSE_FILE=""
if [ -n "$COMPOSE_DIR" ]; then
  for f in docker-compose.yml compose.yaml deploy/compose.yaml; do
    [ -f "$COMPOSE_DIR/$f" ] && { COMPOSE_FILE="$COMPOSE_DIR/$f"; break; }
  done
fi

MODE="host"
HANKO_SVC=""
if have docker && [ -n "$COMPOSE_FILE" ] && docker compose -f "$COMPOSE_FILE" ps --services >/dev/null 2>&1; then
  HANKO_SVC="$(docker compose -f "$COMPOSE_FILE" ps --services 2>/dev/null | grep -E '^hanko$' | head -1)"
  [ -n "$HANKO_SVC" ] && MODE="compose"
fi
if [ "$MODE" = "host" ] && have docker; then
  C="$(docker ps --format '{{.Names}}' 2>/dev/null | grep -i hanko | grep -viE 'postgres|lgtm|memory|caddy' | head -1)"
  [ -n "$C" ] && { MODE="docker"; HANKO_SVC="$C"; }
fi
echo "modo detectado: $MODE   compose: ${COMPOSE_FILE:-nenhum}   container: ${HANKO_SVC:-nenhum}"

# hk: roda o CLI do hanko no lugar certo, repassando credencial e empresa para dentro do container
hk() {
  case "$MODE" in
    compose) docker compose -f "$COMPOSE_FILE" exec -T \
               -e "HANKO_TOKEN=${HANKO_TOKEN:-}" -e "HANKO_COMPANY=${HANKO_COMPANY:-}" -e "HANKO_URL=${HANKO_URL:-}" \
               "$HANKO_SVC" hanko "$@" ;;
    docker)  docker exec \
               -e "HANKO_TOKEN=${HANKO_TOKEN:-}" -e "HANKO_COMPANY=${HANKO_COMPANY:-}" -e "HANKO_URL=${HANKO_URL:-}" \
               "$HANKO_SVC" hanko "$@" ;;
    *)       hanko "$@" ;;
  esac
}
# cx: roda um comando no container (ou no host)
cx() {
  case "$MODE" in
    compose) docker compose -f "$COMPOSE_FILE" exec -T "$HANKO_SVC" "$@" ;;
    docker)  docker exec "$HANKO_SVC" "$@" ;;
    *)       "$@" ;;
  esac
}

# ---------- 1. versão e config ----------
sec "1. Versão"
hk version 2>&1 || echo "(hanko version falhou)"
if [ "$MODE" = "host" ]; then command -v hanko; fi

sec "2. Variáveis HANKO_* / OTEL_* (mascaradas)"
case "$MODE" in
  host)
    env | grep -E '^(HANKO_|OTEL_|ANTHROPIC_|OPENAI_)' | sort | mask
    for f in /etc/hanko/hanko.env "$HOME/.config/hanko/hanko.env" "$HOME/.hanko/hanko.env" ./.env; do
      [ -f "$f" ] && { echo "--- $f"; grep -E '^(HANKO_|OTEL_|ANTHROPIC_|OPENAI_)' "$f" | mask; }
    done
    for f in "${HANKO_CONFIG:-}" ./hanko.yaml "$HOME/.config/hanko/hanko.yaml" /etc/hanko/hanko.yaml; do
      [ -n "$f" ] && [ -f "$f" ] && { echo "--- $f"; mask < "$f"; }
    done
    if have systemctl; then
      echo "--- systemd"; systemctl status hanko --no-pager 2>/dev/null | head -15
      systemctl --user status hanko --no-pager 2>/dev/null | head -15
    fi
    ;;
  *)
    cx env | grep -E '^(HANKO_|OTEL_|ANTHROPIC_|OPENAI_|HOME=|USER=|PATH=)' | sort | mask
    [ -f "$COMPOSE_DIR/.env" ] && { echo "--- $COMPOSE_DIR/.env"; grep -vE '^\s*#|^\s*$' "$COMPOSE_DIR/.env" | mask; }
    echo "--- imagem/volumes/user do container"
    docker inspect "$(cx hostname 2>/dev/null || echo "$HANKO_SVC")" --format 'Image={{.Config.Image}} User={{.Config.User}}' 2>/dev/null
    docker compose -f "$COMPOSE_FILE" config 2>/dev/null | sed -n '/^services:/,$p' | grep -nE '^  [a-z]|image:|user:|volumes:|^      - |HANKO_WORKSPACES_ROOT|HANKO_AGENT_URL|HANKO_PUBLIC_URL|HANKO_MODE|HANKO_MEMORY' | mask
    ;;
esac

# ---------- 3. doctor ----------
sec "3. hanko doctor"
hk doctor 2>&1 || echo "(doctor saiu com erro: $?)"

# ---------- 4. binários e logins no ambiente de execução ----------
sec "4. Harness e git no ambiente onde a persona roda"
for b in claude codex git docker ssh; do
  printf '%-8s ' "$b"; cx sh -c "command -v $b 2>/dev/null || echo AUSENTE" 2>/dev/null || echo "(sem sh no container? distroless)"
done
cx sh -c 'echo "HOME=$HOME"; ls -la "$HOME/.claude" "$HOME/.codex" 2>&1 | head -20' 2>/dev/null
cx sh -c 'claude --version 2>&1 | head -2; claude auth status 2>&1 | head -5' 2>/dev/null

# ---------- 5. logs do servidor ----------
sec "5. Log do servidor (últimas $LOG_LINES linhas, mascaradas)"
LOGTMP="$(mktemp)"
case "$MODE" in
  compose) docker compose -f "$COMPOSE_FILE" logs --no-color --tail "$LOG_LINES" "$HANKO_SVC" 2>&1 > "$LOGTMP" ;;
  docker)  docker logs --tail "$LOG_LINES" "$HANKO_SVC" > "$LOGTMP" 2>&1 ;;
  *)
    if have journalctl; then
      journalctl -u hanko -n "$LOG_LINES" --no-pager -o cat > "$LOGTMP" 2>/dev/null
      [ -s "$LOGTMP" ] || journalctl --user -u hanko -n "$LOG_LINES" --no-pager -o cat > "$LOGTMP" 2>/dev/null
    fi
    [ -s "$LOGTMP" ] || { ls -t /var/log/hanko* "$HOME"/hanko*.log 2>/dev/null | head -1 | xargs -r tail -n "$LOG_LINES" > "$LOGTMP"; }
    [ -s "$LOGTMP" ] || echo "(não achei o log: rode com HANKO_LOG_FILE=/caminho/do/log, ou cole as últimas linhas à mão)"
    [ -n "${HANKO_LOG_FILE:-}" ] && tail -n "$LOG_LINES" "$HANKO_LOG_FILE" > "$LOGTMP"
    ;;
esac
echo "--- contagem por nível"
grep -oE '"level":"[A-Z]+"|level=[A-Z]+' "$LOGTMP" | sort | uniq -c | sort -rn
echo "--- WARN/ERROR agrupados por mensagem (top 40)"
grep -E '"level":"(WARN|ERROR)"|level=(WARN|ERROR)' "$LOGTMP" \
  | sed -E 's/.*"msg":"([^"]*)".*/\1/; s/.*msg="?([^"]*)"?.*/\1/' | sort | uniq -c | sort -rn | head -40
echo "--- últimas 120 linhas WARN/ERROR"
grep -E '"level":"(WARN|ERROR)"|level=(WARN|ERROR)' "$LOGTMP" | tail -120 | mask
echo "--- boot (primeiras linhas com 'hanko' / 'servindo' / 'modo')"
grep -iE 'servindo|listening|modo |HANKO_SECRET|ai-memory|OpenTelemetry|trusted|claim' "$LOGTMP" | head -20 | mask
rm -f "$LOGTMP"

# ---------- 6. dados pela API (precisa de credencial em modo authenticated) ----------
sec "6. Empresas, workspaces, projetos, personas, readiness, runs"
echo "(se aparecer 401/'credencial ausente': rode de novo com HANKO_TOKEN=hk_... ou faça 'hanko login' antes)"
hk whoami 2>&1 | head -5
hk company list --json 2>&1 | head -60
if [ -z "${HANKO_COMPANY:-}" ]; then
  HANKO_COMPANY="$(hk company list --json 2>/dev/null | jfield slug | head -1)"
  export HANKO_COMPANY
fi
echo "empresa usada: ${HANKO_COMPANY:-<nenhuma>}"
if [ -n "${HANKO_COMPANY:-}" ]; then
  echo "--- member list"; hk member list 2>&1 | head -20
  echo "--- workspace list"; hk workspace list --json 2>&1 | head -80
  echo "--- project list";   hk project list 2>&1 | head -30
  echo "--- persona list";   hk persona list --json 2>&1 | head -120
  echo "--- readiness";      hk persona readiness 2>&1 | head -40
  echo "--- env list";       hk env list 2>&1 | head -20
  echo "--- últimas 15 runs"; hk run list --limit 15 2>&1
  echo "--- runs failed (até 5) com log"
  for r in $(hk run list --status failed --limit 5 2>/dev/null | uuids); do
    echo "##### run $r"; hk run show "$r" 2>&1 | head -30; hk run logs "$r" 2>&1 | mask | tail -80
  done
  echo "--- últimas 3 runs (qualquer status): linhas de workspace, permissão, política e erro"
  for r in $(hk run list --limit 3 2>/dev/null | uuids); do
    echo "##### run $r"; hk run show "$r" 2>&1 | head -25
    hk run logs "$r" 2>&1 | mask | grep -iE 'workspace|worktree|diretório|permiss|política|negad|denied|failed|erro|error|warn|tool' | tail -60
  done
fi

# ---------- 7. disco: raiz e caminhos dos workspaces ----------
sec "7. Raiz dos workspaces e caminhos cadastrados (no ambiente de execução)"
ROOT="$(cx sh -c 'echo ${HANKO_WORKSPACES_ROOT:-$HOME/hanko-workspaces}' 2>/dev/null)"
echo "HANKO_WORKSPACES_ROOT efetiva: $ROOT"
cx sh -c "ls -la '$ROOT' 2>&1; echo; ls -la '$ROOT'/* 2>&1 | head -40" 2>/dev/null
PATHS="$(hk workspace list --json 2>/dev/null | jfield path)"
for p in $PATHS; do
  echo "----- $p"
  cx sh -c "
    if [ -e '$p' ]; then
      echo 'existe: sim'; ls -ld '$p'; readlink -f '$p' 2>/dev/null && echo '(real acima)';
      [ -r '$p' ] && echo 'legível: sim' || echo 'legível: NÃO';
      [ -w '$p' ] && echo 'gravável: sim' || echo 'gravável: NÃO';
      [ -x '$p' ] && echo 'entrável: sim' || echo 'entrável: NÃO';
      ls '$p' 2>&1 | head -5; [ -d '$p/.git' ] && (cd '$p' && git status -sb 2>&1 | head -3);
    else
      echo 'existe: NÃO'; d='$p'; while [ \"\$d\" != / ] && [ ! -e \"\$d\" ]; do d=\$(dirname \"\$d\"); done; echo \"primeiro pai existente: \$d\"; ls -ld \"\$d\";
    fi" 2>/dev/null || echo "(sem shell no container para inspecionar; imagem distroless?)"
  if [ "$MODE" != "host" ]; then
    echo "no HOST:"; [ -e "$p" ] && ls -ld "$p" || echo "  não existe no host (esperado se o volume é só do container)"
  fi
done

# ---------- 8. rede entre persona e API ----------
sec "8. A persona alcança a API do Hanko? (hook de política de tools)"
AURL="$(cx sh -c 'echo ${HANKO_AGENT_URL:-${HANKO_PUBLIC_URL:-http://127.0.0.1:7420}}' 2>/dev/null)"
echo "URL usada pelo harness: $AURL"
echo "(o próprio CLI faz a chamada, como o hook pretooluse; 'autenticação necessária' ou 'whoami' ok = alcança; 'connection refused' = não)"
HANKO_URL="$AURL" hk whoami 2>&1 | head -3
echo "--- ai-memory (o doctor acima já testa; aqui só a URL)"
cx sh -c 'echo ${HANKO_MEMORY_URL:-http://127.0.0.1:49374}' 2>/dev/null

# ---------- 9. Git dos workspaces (ADR-0054): espelhos e worktrees ----------
sec "9. Git dos workspaces: espelhos em .hanko/mirrors e worktrees em .hanko/worktrees"
GITWS="$(cx sh -c 'echo ${HANKO_GIT_WORKSPACES:-}' 2>/dev/null)"
echo "HANKO_GIT_WORKSPACES=${GITWS:-<vazio>}"
cx sh -c "for d in '$ROOT'/*/.hanko; do [ -d \"\$d\" ] || continue; echo '--- '\$d; find \"\$d\" -maxdepth 3 -not -path '*/objects/*' -not -path '*/refs/*' 2>&1 | head -40; done" 2>/dev/null
if [ -n "${HANKO_COMPANY:-}" ]; then
  for s in $(hk workspace list --json 2>/dev/null | jfield slug); do
    echo "===== workspace $s"
    hk workspace show "$s" 2>&1 | head -20
    hk workspace git show "$s" 2>&1 | mask | head -25
  done
fi

sec "FIM — arquivo: $OUT"
