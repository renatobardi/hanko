#!/usr/bin/env bash
# Teste de scripts/release-notes.sh contra um CHANGELOG e fragmentos de mentira (roda no ci).
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
script=$here/release-notes.sh
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

export CHANGELOG_FILE=$work/CHANGELOG.md
export CHANGELOG_DIR=$work/changelog.d
mkdir -p "$CHANGELOG_DIR"

cat >"$CHANGELOG_FILE" <<'EOF'
# Changelog

## [Unreleased]

- item antigo do unreleased

## [0.2.0] — 2026-10-15

- algo do 0.2.0
EOF
printf -- '- fragmento A\n- English summary: a\n' >"$CHANGELOG_DIR/a.md"
printf -- '\n- fragmento B\n\n' >"$CHANGELOG_DIR/b.md"
printf 'ignorado\n' >"$CHANGELOG_DIR/README.md"

fail() {
  echo "FALHOU: $*" >&2
  exit 1
}

# prévia = unreleased + fragmentos, sem README
out=$("$script" notes)
grep -q 'item antigo do unreleased' <<<"$out" || fail "prévia sem o unreleased"
grep -q 'fragmento A' <<<"$out" || fail "prévia sem o fragmento A"
grep -q 'fragmento B' <<<"$out" || fail "prévia sem o fragmento B"
grep -q 'ignorado' <<<"$out" && fail "README.md não deveria entrar"

# notas de versão existente
out=$("$script" notes v0.2.0)
[ "$out" = "- algo do 0.2.0" ] || fail "notas do 0.2.0 = [$out]"

# versão inexistente, versão inválida
"$script" notes 9.9.9 2>/dev/null && fail "versão inexistente deveria falhar"
"$script" notes abc 2>/dev/null && fail "versão inválida deveria falhar"

# dry-run não muda nada
"$script" cut 0.3.0 --date 2026-11-01 --dry-run >/dev/null
[ -e "$CHANGELOG_DIR/a.md" ] || fail "dry-run removeu fragmento"
grep -q '0.3.0' "$CHANGELOG_FILE" && fail "dry-run alterou o CHANGELOG"

# corte de verdade
"$script" cut 0.3.0 --date 2026-11-01 2>/dev/null
[ -e "$CHANGELOG_DIR/a.md" ] && fail "fragmento deveria ter sido removido"
[ -e "$CHANGELOG_DIR/README.md" ] || fail "README.md deveria ficar"
out=$("$script" notes 0.3.0)
for want in 'item antigo do unreleased' 'fragmento A' 'fragmento B'; do
  grep -q "$want" <<<"$out" || fail "seção 0.3.0 sem '$want'"
done
# ordem: Unreleased (vazio) → 0.3.0 → 0.2.0
order=$(grep -n '^## \[' "$CHANGELOG_FILE" | sed 's/^[0-9]*://' | tr '\n' '|')
[ "$order" = '## [Unreleased]|## [0.3.0] — 2026-11-01|## [0.2.0] — 2026-10-15|' ] || fail "ordem das seções: $order"
[ "$("$script" notes 0.2.0)" = "- algo do 0.2.0" ] || fail "0.2.0 foi alterada"

# repetir o corte falha (já existe; nada a lançar)
"$script" cut 0.3.0 2>/dev/null && fail "cortar a mesma versão deveria falhar"
"$script" cut 0.4.0 2>/dev/null && fail "cortar sem nada pendente deveria falhar"
"$script" notes 2>/dev/null && fail "prévia sem nada pendente deveria falhar"

echo "release-notes: ok"
