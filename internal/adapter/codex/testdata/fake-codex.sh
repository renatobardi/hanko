#!/usr/bin/env bash
# Fake `codex exec --json` for tests. Debug info goes to stderr.
#   FAKE_CODEX_FAIL=1   -> emits turn.failed and exits 1
#   FAKE_CODEX_SLEEP=N  -> sleeps N seconds after thread.started
set -u
echo "ARGS: $*" >&2
echo "STDIN: $(cat | tr '\n' '~')" >&2
echo "ENV_TOKEN: ${HANKO_MCP_TOKEN_HANKO:-}" >&2

echo '{"type":"thread.started","thread_id":"thr-1"}'
echo '{"type":"turn.started"}'
if [ -n "${FAKE_CODEX_SLEEP:-}" ]; then sleep "$FAKE_CODEX_SLEEP" & wait $!; fi
echo '{"type":"item.completed","item":{"id":"item_0","type":"reasoning","text":"Vou inspecionar o diretório."}}'
echo '{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"bash -lc ls","aggregated_output":"","exit_code":null,"status":"in_progress"}}'
echo '{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"bash -lc ls","aggregated_output":"README.md\n","exit_code":0,"status":"completed"}}'
echo '{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"Rascunho."}}'
if [ "${FAKE_CODEX_FAIL:-}" = "1" ]; then
  echo '{"type":"turn.failed","error":{"message":"falha simulada"}}'
  exit 1
fi
echo '{"type":"item.completed","item":{"id":"item_3","type":"agent_message","text":"Listei os arquivos."}}'
echo '{"type":"turn.completed","usage":{"input_tokens":120,"cached_input_tokens":20,"output_tokens":40}}'
