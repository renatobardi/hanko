#!/usr/bin/env bash
# Notas de release a partir do CHANGELOG.md e de changelog.d/*.md (#60). Fluxo completo em docs/RELEASING.md.
#
#   scripts/release-notes.sh notes [VERSÃO]
#       Imprime as notas. Com VERSÃO (1.2.3 ou v1.2.3): a seção "## [1.2.3]" do CHANGELOG.md (é o que o
#       release.yml publica). Sem VERSÃO: a prévia do próximo corte = seção [Unreleased] + fragmentos de changelog.d.
#
#   scripts/release-notes.sh cut VERSÃO [--date AAAA-MM-DD] [--dry-run]
#       Corta a versão: funde [Unreleased] + changelog.d/*.md numa seção nova "## [VERSÃO] — data" no topo do
#       CHANGELOG.md, deixa [Unreleased] vazio e remove os fragmentos. Não faz commit nem tag.
#       --dry-run só imprime a seção que seria escrita.
#
# Fragmentos: um arquivo .md por marco em changelog.d/ (README.md é ignorado), com marcadores "- ..." em pt-BR
# e uma linha "English summary: ...". Variáveis para teste: CHANGELOG_FILE, CHANGELOG_DIR.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
changelog=${CHANGELOG_FILE:-$root/CHANGELOG.md}
fragdir=${CHANGELOG_DIR:-$root/changelog.d}

die() {
  echo "release-notes: $*" >&2
  exit 2
}

usage() {
  sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//' >&2
  exit 2
}

# valida SemVer (com pré-release opcional) e tira o "v" inicial
semver() {
  local v=${1#v}
  [[ $v =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ ]] || die "versão inválida (esperado SemVer, ex.: 0.3.0): $1"
  printf '%s' "$v"
}

# corpo da seção "## [título]" do CHANGELOG, sem linhas em branco nas pontas
section() {
  awk -v want="## [$1]" '
    /^## \[/ { insec = (index($0, want) == 1); next }
    insec { print }
  ' "$changelog" | awk '
    { lines[NR] = $0 }
    END {
      s = 1; e = NR
      while (s <= e && lines[s] ~ /^[[:space:]]*$/) s++
      while (e >= s && lines[e] ~ /^[[:space:]]*$/) e--
      for (i = s; i <= e; i++) print lines[i]
    }'
}

has_section() { grep -q -F "## [$1]" "$changelog"; }

# fragmentos, em ordem de nome, separados por linha em branco
fragments() {
  local f first=1
  [ -d "$fragdir" ] || return 0
  for f in "$fragdir"/*.md; do
    [ -e "$f" ] || continue
    [ "$(basename "$f")" = README.md ] && continue
    [ "$first" -eq 1 ] || echo
    first=0
    awk '{ lines[NR] = $0 } END { s = 1; e = NR; while (s <= e && lines[s] ~ /^[[:space:]]*$/) s++; while (e >= s && lines[e] ~ /^[[:space:]]*$/) e--; for (i = s; i <= e; i++) print lines[i] }' "$f"
  done
}

# Unreleased + fragmentos
pending() {
  local u f
  u=$(section Unreleased)
  f=$(fragments)
  if [ -n "$u" ] && [ -n "$f" ]; then
    printf '%s\n\n%s\n' "$u" "$f"
  elif [ -n "$u$f" ]; then
    printf '%s\n' "$u$f"
  fi
}

cmd_notes() {
  [ -f "$changelog" ] || die "não achei $changelog"
  if [ $# -ge 1 ]; then
    local v out
    v=$(semver "$1")
    out=$(section "$v")
    [ -n "$out" ] || die "o CHANGELOG não tem a seção [$v] (rode 'cut $v' antes de taguear)"
    printf '%s\n' "$out"
  else
    local out
    out=$(pending)
    [ -n "$out" ] || die "nada a lançar: [Unreleased] vazio e changelog.d sem fragmentos"
    printf '%s\n' "$out"
  fi
}

cmd_cut() {
  [ $# -ge 1 ] || usage
  local v date dry=0
  v=$(semver "$1")
  shift
  date=$(date -u +%F)
  while [ $# -gt 0 ]; do
    case "$1" in
      --date)
        [ $# -ge 2 ] || die "--date pede um valor"
        date=$2
        shift 2
        ;;
      --dry-run)
        dry=1
        shift
        ;;
      *) die "argumento desconhecido: $1" ;;
    esac
  done
  [[ $date =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || die "data inválida: $date"
  [ -f "$changelog" ] || die "não achei $changelog"
  grep -q '^## \[Unreleased\]' "$changelog" || die "o CHANGELOG não tem a seção [Unreleased]"
  has_section "$v" && die "o CHANGELOG já tem a seção [$v]"

  local body tmp bodyfile
  body=$(pending)
  [ -n "$body" ] || die "nada a lançar: [Unreleased] vazio e changelog.d sem fragmentos"

  if [ "$dry" -eq 1 ]; then
    printf '## [%s] — %s\n\n%s\n' "$v" "$date" "$body"
    return 0
  fi

  tmp=$(mktemp)
  bodyfile=$(mktemp)
  trap 'rm -f "$tmp" "$bodyfile"' RETURN
  printf '%s\n' "$body" >"$bodyfile"
  # copia tudo até o cabeçalho [Unreleased], esvazia a seção e insere a versão nova antes da próxima "## ["
  awk -v ver="$v" -v date="$date" -v bodyfile="$bodyfile" '
    function emit(   line) {
      printf "## [%s] — %s\n\n", ver, date
      while ((getline line < bodyfile) > 0) print line
      close(bodyfile)
      print ""
    }
    /^## \[Unreleased\]/ { print; print ""; state = 1; next }
    state == 1 && /^## \[/ { emit(); state = 2 }
    state == 1 { next } # descarta o corpo antigo do [Unreleased]
    { print }
    END { if (state == 1) emit() } # [Unreleased] era a última seção
  ' "$changelog" >"$tmp"
  cat "$tmp" >"$changelog"

  local f
  for f in "$fragdir"/*.md; do
    [ -e "$f" ] || continue
    [ "$(basename "$f")" = README.md ] && continue
    rm -f "$f"
  done
  echo "release-notes: CHANGELOG.md atualizado com [$v] — $date; fragmentos removidos. Revise, faça commit e crie a tag v$v." >&2
}

case "${1:-}" in
  notes)
    shift
    cmd_notes "$@"
    ;;
  cut)
    shift
    cmd_cut "$@"
    ;;
  *) usage ;;
esac
