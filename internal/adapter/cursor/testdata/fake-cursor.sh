#!/usr/bin/env bash
# Fake `cursor-agent -p --output-format stream-json` for tests: replays testdata/run.jsonl.
#   FAKE_CURSOR_FAIL=1  -> emits a result with is_error and exits 1
set -u
here="$(cd "$(dirname "$0")" && pwd)"
echo "ARGS: $*" >&2
echo "STDIN: $(cat | tr '\n' '~')" >&2
echo "ENV: token=${HANKO_TOKEN:-}|company=${HANKO_COMPANY:-}|url=${HANKO_URL:-}" >&2
if [ "${FAKE_CURSOR_FAIL:-}" = "1" ]; then
  echo '{"type":"system","subtype":"init","session_id":"cur-err","model":"x"}'
  echo '{"type":"result","subtype":"error","is_error":true,"result":"Authentication required: run cursor-agent login","session_id":"cur-err"}'
  exit 1
fi
cat "$here/run.jsonl"
