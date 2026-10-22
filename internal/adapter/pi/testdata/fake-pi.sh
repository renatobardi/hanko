#!/usr/bin/env bash
# Fake `pi --mode json -p` for tests: replays testdata/run.jsonl. Debug info goes to stderr.
#   FAKE_PI_FAIL=1   -> emits an assistant message with stopReason "error" and exits 1
#   FAKE_PI_EMPTY=1  -> exits 0 with no output
set -u
here="$(cd "$(dirname "$0")" && pwd)"
echo "ARGS: $*" >&2
echo "STDIN: $(cat | tr '\n' '~')" >&2
echo "ENV: token=${HANKO_TOKEN:-}|company=${HANKO_COMPANY:-}|url=${HANKO_URL:-}|runtoken=${HANKO_RUN_TOKEN:-}" >&2
echo "ENV_LEAK: secret=${HANKO_SECRET:-}|db=${HANKO_DATABASE_URL:-}" >&2

if [ "${FAKE_PI_EMPTY:-}" = "1" ]; then exit 0; fi
if [ "${FAKE_PI_FAIL:-}" = "1" ]; then
  echo '{"type":"session","id":"pi-sess-err"}'
  echo '{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"401 invalid api key","content":[]}}'
  exit 1
fi
cat "$here/run.jsonl"
