#!/usr/bin/env bash
# Teste de scripts/install.sh contra uma "release" de mentira em file:// (sem rede; roda no ci).
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
script=$here/install.sh
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() {
  echo "FALHOU: $*" >&2
  exit 1
}

sum() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

# release de mentira: v9.9.9 para linux/amd64 e darwin/arm64
rel=$work/releases/download/v9.9.9
mkdir -p "$rel"
for pair in linux_amd64 darwin_arm64; do
  stage=$work/stage_$pair
  mkdir -p "$stage"
  printf '#!/bin/sh\necho hanko-fake %s\n' "$pair" >"$stage/hanko"
  chmod +x "$stage/hanko"
  tar -czf "$rel/hanko_9.9.9_$pair.tar.gz" -C "$stage" hanko
done
{
  for f in "$rel"/*.tar.gz; do printf '%s  %s\n' "$(sum "$f")" "$(basename "$f")"; done
} >"$rel/checksums.txt"

export HANKO_RELEASES_URL=file://$work/releases
export HANKO_VERSION=v9.9.9

# 1. instala linux/amd64 e o binário roda
dest=$work/bin1
HANKO_OS=linux HANKO_ARCH=x86_64 HANKO_INSTALL_DIR=$dest sh "$script" >"$work/out1" 2>&1 || { cat "$work/out1"; fail "instalação linux/amd64"; }
[ -x "$dest/hanko" ] || fail "binário não instalado"
[ "$("$dest/hanko")" = "hanko-fake linux_amd64" ] || fail "binário errado: $("$dest/hanko")"
[ ! -e "$dest/.hanko.new" ] || fail "sobrou arquivo temporário"

# 2. darwin/arm64 (aarch64 também vale) e versão sem o "v"
dest=$work/bin2
HANKO_VERSION=9.9.9 HANKO_OS=darwin HANKO_ARCH=arm64 HANKO_INSTALL_DIR=$dest sh "$script" >/dev/null 2>&1 || fail "instalação darwin/arm64"
[ "$("$dest/hanko")" = "hanko-fake darwin_arm64" ] || fail "binário darwin errado"

# 3. dry-run não baixa nem escreve
dest=$work/bin3
out=$(HANKO_DRY_RUN=1 HANKO_OS=linux HANKO_ARCH=amd64 HANKO_INSTALL_DIR=$dest sh "$script")
case $out in *"hanko_9.9.9_linux_amd64.tar.gz"*) ;; *) fail "dry-run sem o nome do pacote: $out" ;; esac
[ ! -e "$dest" ] || fail "dry-run escreveu no destino"

# 4. sha256 adulterado é recusado e não instala nada
dest=$work/bin4
cp -r "$rel" "$work/bad"
mkdir -p "$work/badreleases/download" && mv "$work/bad" "$work/badreleases/download/v9.9.9"
sed -i.bak 's/^[0-9a-f]\{8\}/00000000/' "$work/badreleases/download/v9.9.9/checksums.txt"
if HANKO_RELEASES_URL=file://$work/badreleases HANKO_OS=linux HANKO_ARCH=amd64 HANKO_INSTALL_DIR=$dest sh "$script" >"$work/out4" 2>&1; then
  fail "aceitou checksum adulterado"
fi
grep -q "sha256 diferente" "$work/out4" || { cat "$work/out4"; fail "mensagem de checksum ausente"; }
[ ! -e "$dest/hanko" ] || fail "instalou apesar do checksum"

# 5. plataforma sem binário
if HANKO_OS=windows HANKO_ARCH=amd64 HANKO_INSTALL_DIR=$work/bin5 sh "$script" >"$work/out5" 2>&1; then fail "aceitou windows"; fi
grep -q "não suportado" "$work/out5" || fail "mensagem de sistema não suportado ausente"
if HANKO_OS=linux HANKO_ARCH=mips HANKO_INSTALL_DIR=$work/bin5 sh "$script" >"$work/out5" 2>&1; then fail "aceitou mips"; fi

# 6. pacote que não existe na release
if HANKO_OS=linux HANKO_ARCH=arm64 HANKO_INSTALL_DIR=$work/bin6 sh "$script" >"$work/out6" 2>&1; then fail "aceitou pacote ausente"; fi
[ ! -e "$work/bin6/hanko" ] || fail "instalou pacote ausente"

echo "ok: install.sh"
