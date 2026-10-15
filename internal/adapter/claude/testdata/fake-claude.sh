#!/usr/bin/env bash
# Fake `claude -p --output-format stream-json --verbose` for tests.
# Debug info (args, cwd, stdin, mcp config) goes to stderr.
#   FAKE_CLAUDE_FAIL=1   -> emits an error result and exits 1
#   FAKE_CLAUDE_SLEEP=N  -> sleeps N seconds after init (cancellation tests)
set -u

echo "ARGS: $*" >&2
echo "CWD: $(pwd)" >&2
echo "ENV_FOO: ${FOO:-}" >&2
echo "ENV_PERSONA: ${ANTHROPIC_BASE_URL:-}|${ANTHROPIC_AUTH_TOKEN:-}|${BAR:-}" >&2
echo "ENV_LEAK: secret=${HANKO_SECRET:-}|db=${HANKO_DATABASE_URL:-}|mem=${HANKO_MEMORY_TOKEN:-}|aws=${AWS_SECRET_ACCESS_KEY:-}|run=${HANKO_RUN_TOKEN:-}|path=${PATH:+set}" >&2

prev=""
for a in "$@"; do
  if [ "$prev" = "--mcp-config" ] && [ -f "$a" ]; then
    echo "MCPFILE: $a" >&2
    echo "MCP: $(tr -d '\n' < "$a")" >&2
  fi
  if [ "$prev" = "--add-dir" ] && [ -d "$a" ]; then
    echo "ADDDIR: $a" >&2
    for f in "$a"/.claude/skills/*/SKILL.md; do
      [ -f "$f" ] && echo "SKILL: $f :: $(tr '\n' '~' < "$f")" >&2
    done
  fi
  prev="$a"
done

stdin="$(cat)"
echo "STDIN: $(printf '%s' "$stdin" | head -n1)" >&2

echo 'not json: warming up'
echo '{"type":"system","subtype":"init","cwd":"/work","session_id":"sess-123","model":"claude-sonnet-fake","tools":["Bash","Read"],"permissionMode":"acceptEdits"}'

if [ -n "${FAKE_CLAUDE_SLEEP:-}" ]; then
  # exec so the group has a grandchild-free leader that dies on SIGTERM
  sleep "$FAKE_CLAUDE_SLEEP" &
  wait $!
fi

echo '{"type":"assistant","message":{"id":"msg_1","role":"assistant","model":"claude-sonnet-fake","content":[{"type":"thinking","thinking":"Preciso ler o arquivo primeiro."},{"type":"text","text":"Vou listar os arquivos."},{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls -la","description":"lista arquivos"}}]},"session_id":"sess-123"}'
echo '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"total 0\nREADME.md","is_error":false}]},"session_id":"sess-123"}'
if [ -n "${HANKO_RUN_TOKEN:-}" ]; then
  echo "{\"type\":\"assistant\",\"message\":{\"id\":\"msg_leak\",\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"token ${HANKO_RUN_TOKEN}\"}]},\"session_id\":\"sess-123\"}"
fi
echo '{"type":"assistant","message":{"id":"msg_2","role":"assistant","content":[{"type":"text","text":"Pronto."}]},"session_id":"sess-123"}'

if [ "${FAKE_CLAUDE_FAIL:-}" = "1" ]; then
  echo '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"falha simulada","session_id":"sess-123","total_cost_usd":0.001,"usage":{"input_tokens":1,"output_tokens":1}}'
  echo "boom" >&2
  exit 1
fi

echo '{"type":"result","subtype":"success","is_error":false,"duration_ms":1200,"num_turns":2,"result":"Listei os arquivos. Próximo passo: revisar o README.","session_id":"sess-123","total_cost_usd":0.0123,"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":10,"cache_creation_input_tokens":5}}'
