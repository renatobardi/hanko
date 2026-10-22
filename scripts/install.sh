#!/bin/sh
# Instala o binário `hanko` de uma release do GitHub (linux/darwin, amd64/arm64), verificando o sha256.
#
#   curl -fsSL https://raw.githubusercontent.com/renatobardi/hanko/main/scripts/install.sh | sh
#   HANKO_VERSION=0.4.0 HANKO_INSTALL_DIR=$HOME/bin sh install.sh
#
# Variáveis (todas opcionais):
#   HANKO_VERSION      versão a instalar, com ou sem "v" (padrão: a última release)
#   HANKO_INSTALL_DIR  destino do binário (padrão: /usr/local/bin se gravável, senão ~/.local/bin)
#   HANKO_RELEASES_URL base das releases (padrão: https://github.com/renatobardi/hanko/releases);
#                      aceita file:// para testes e espelhos internos
#   HANKO_OS, HANKO_ARCH  sobrepõem a detecção (testes)
#   HANKO_DRY_RUN=1    só mostra o que faria
#
# O script só baixa da base acima; não pede root nem escreve fora de HANKO_INSTALL_DIR.
set -eu

BASE=${HANKO_RELEASES_URL:-https://github.com/renatobardi/hanko/releases}
BASE=${BASE%/}

say() { printf '%s\n' "$*"; }
die() {
  printf 'install.sh: %s\n' "$*" >&2
  exit 1
}

fetch() { # fetch URL DEST
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 3 -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$2" "$1"
  else
    die "preciso de curl ou wget"
  fi
}

detect_os() {
  os=${HANKO_OS:-$(uname -s | tr '[:upper:]' '[:lower:]')}
  case $os in
    linux | darwin) printf '%s' "$os" ;;
    *) die "sistema não suportado: $os (há binários para linux e darwin)" ;;
  esac
}

detect_arch() {
  arch=${HANKO_ARCH:-$(uname -m)}
  case $arch in
    x86_64 | amd64) printf 'amd64' ;;
    aarch64 | arm64) printf 'arm64' ;;
    *) die "arquitetura não suportada: $arch (há binários para amd64 e arm64)" ;;
  esac
}

latest_version() {
  command -v curl >/dev/null 2>&1 || die "sem HANKO_VERSION preciso do curl para descobrir a última release"
  url=$(curl -fsSL -o /dev/null -w '%{url_effective}' "$BASE/latest") || die "não consegui descobrir a última release"
  tag=${url##*/}
  [ -n "$tag" ] && [ "$tag" != latest ] || die "não consegui descobrir a última release"
  printf '%s' "$tag"
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d' ' -f1
  else
    die "preciso de sha256sum ou shasum para verificar o download"
  fi
}

pick_dir() {
  if [ -n "${HANKO_INSTALL_DIR:-}" ]; then
    printf '%s' "$HANKO_INSTALL_DIR"
  elif [ -w /usr/local/bin ]; then
    printf '/usr/local/bin'
  else
    printf '%s/.local/bin' "${HOME:?HOME não definido}"
  fi
}

main() {
  os=$(detect_os)
  arch=$(detect_arch)
  tag=${HANKO_VERSION:-$(latest_version)}
  tag=v${tag#v}
  version=${tag#v}
  asset="hanko_${version}_${os}_${arch}.tar.gz"
  dir=$(pick_dir)
  say "hanko $tag ($os/$arch) -> $dir"
  if [ "${HANKO_DRY_RUN:-}" = 1 ]; then
    say "dry-run: baixaria $BASE/download/$tag/$asset"
    return 0
  fi

  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT INT TERM
  fetch "$BASE/download/$tag/$asset" "$tmp/$asset" || die "falha ao baixar $asset"
  fetch "$BASE/download/$tag/checksums.txt" "$tmp/checksums.txt" || die "falha ao baixar checksums.txt"

  want=$(awk -v f="$asset" '$2 == f { print $1 }' "$tmp/checksums.txt")
  [ -n "$want" ] || die "$asset não consta em checksums.txt"
  got=$(sha256_of "$tmp/$asset")
  [ "$want" = "$got" ] || die "sha256 diferente para $asset (esperado $want, obtido $got)"

  tar -xzf "$tmp/$asset" -C "$tmp" hanko || die "o pacote não contém o binário hanko"
  mkdir -p "$dir"
  # copia e renomeia: um mv atômico no mesmo diretório não deixa um binário pela metade
  cp "$tmp/hanko" "$dir/.hanko.new"
  chmod 0755 "$dir/.hanko.new"
  mv -f "$dir/.hanko.new" "$dir/hanko"
  say "instalado: $dir/hanko"
  case ":$PATH:" in
    *":$dir:"*) ;;
    *) say "aviso: $dir não está no PATH; acrescente-o ao seu shell" ;;
  esac
}

main "$@"
