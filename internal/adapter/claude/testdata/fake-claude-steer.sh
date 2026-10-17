#!/usr/bin/env bash
# Fake `claude -p --input-format stream-json --output-format stream-json` for the steering tests.
# Each stdin line is one user turn: it answers with an assistant text and a result, and exits at EOF.
#   FAKE_STEER_HOLD=N -> waits N seconds before answering the first turn (time for a steer to arrive)
set -u
echo "ARGS: $*" >&2
echo '{"type":"system","subtype":"init","session_id":"sess-steer","model":"claude-sonnet-fake"}'
n=0
while IFS= read -r line; do
  n=$((n + 1))
  echo "USERLINE: $line" >&2
  text=$(printf '%s' "$line" | sed -E 's/.*"text":"([^"]*)".*/\1/')
  if [ "$n" = "1" ] && [ -n "${FAKE_STEER_HOLD:-}" ]; then sleep "$FAKE_STEER_HOLD"; fi
  echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_'"$n"'","name":"Bash","input":{"command":"echo '"$n"'"}}]},"session_id":"sess-steer"}'
  echo '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_'"$n"'","content":"ok"}]},"session_id":"sess-steer"}'
  echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"turno '"$n"': '"$text"'"}]},"session_id":"sess-steer"}'
  echo '{"type":"result","subtype":"success","is_error":false,"result":"turno '"$n"' concluido: '"$text"'","session_id":"sess-steer","total_cost_usd":0.01,"usage":{"input_tokens":1,"output_tokens":1}}'
done
