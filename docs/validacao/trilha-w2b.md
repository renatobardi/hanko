# Validação — Trilha contínua, onda 2B (ADR-0060)

Itens: #54 install.sh + tap Homebrew, #52 `hanko mcp`, #58 evals e chaos.

## O que entrou
- **Migrations:** nenhuma. **Dependência Go nova:** nenhuma (usa o SDK MCP e o pgx que já existiam).
- **Env novas:** `HANKO_VERSION`, `HANKO_INSTALL_DIR`, `HANKO_RELEASES_URL`, `HANKO_DRY_RUN` (só `install.sh`);
  `HANKO_EVALS_LIVE`, `HANKO_EVALS_SUITE`, `HANKO_EVALS_TRIALS`, `HANKO_EVALS_MODEL`, `HANKO_EVALS_MAX_USD`, `ANTHROPIC_API_KEY` (só `hanko-evals`).
- **CLI nova:** `hanko mcp`. **Binário novo:** `cmd/hanko-evals` (não vai na release).
- **CI:** `release.yml` repassa o secret opcional ao goreleaser; passo "scripts (install.sh)" no job `check`; `evals.yml` preenchido. `release-dry-run` inalterado e verde
  (`goreleaser check` e `build --snapshot` rodados localmente com a versão mais recente).
- **Variáveis/secrets de repositório (opcionais):** secret `HOMEBREW_TAP_GITHUB_TOKEN`, secret `ANTHROPIC_API_KEY`,
  variáveis `EVALS_LIVE`, `EVALS_MODEL`, `EVALS_MAX_USD`.

## Como validar
1. **install.sh (#54).** `scripts/test-install.sh` (sem rede). Contra uma release real, depois da primeira tag:
   `curl -fsSL https://raw.githubusercontent.com/renatobardi/hanko/main/scripts/install.sh | HANKO_INSTALL_DIR=$HOME/bin sh` e
   `~/bin/hanko version`. `HANKO_DRY_RUN=1` só mostra a URL do pacote. Checksum adulterado ou plataforma sem binário: erro e nada instalado.
2. **Tap (#54).** Antes da primeira tag: criar o repositório `renatobardi/homebrew-tap` (vazio, com branch padrão) e o secret
   `HOMEBREW_TAP_GITHUB_TOKEN` (PAT com escrita nesse repositório) (o `release.yml` já o repassa ao goreleaser).
   Sem o secret a release segue e o cask é pulado. Depois da tag: `brew install --cask renatobardi/tap/hanko`.
   Localmente: `goreleaser release --snapshot --clean --skip=docker,sbom,publish` gera `dist/homebrew/Casks/hanko.rb`.
3. **hanko mcp (#52).** `hanko login` (ou `HANKO_URL`/`HANKO_TOKEN`) e, na IDE, registrar o servidor stdio `hanko mcp`.
   Sem IDE: `printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}' '{"jsonrpc":"2.0","method":"notifications/initialized"}' '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' | hanko mcp`
   deve listar as tools `hanko_*`. Token inválido: a resposta traz o erro 401 do servidor.
4. **Evals (#58).** `go run ./cmd/hanko-evals` (sem custo; verde). `--bad` tem de sair vermelho (código 1).
   `go test -race -tags integration ./internal/evals` roda também o chaos de banco de verdade (precisa de `HANKO_TEST_DATABASE_URL`
   com nome contendo `test`). Modo pago, só de propósito: `HANKO_EVALS_LIVE=1 ANTHROPIC_API_KEY=… go run ./cmd/hanko-evals --suite behavior`
   (teto padrão US$ 1; relatório com `cost.kind = "test"`). Manual no GitHub: Actions › evals › Run workflow, marcando `live`.

## Resultado local
- `scripts/test-install.sh`: ok. `go test -race ./internal/mcpbridge ./internal/evals ./internal/cli`: verde.
- Evals no fake: 6 cenários (4 comportamento, 2 chaos) aprovados; os 6 controles negativos reprovam.
- **Não testado:** modelo real (nenhuma chamada paga feita; o cliente foi exercitado só contra um servidor HTTP de mentira),
  `hanko mcp` contra um Hanko completo com API key (testado contra servidor MCP de mentira e pelo handshake do SDK),
  publicação no tap e download de uma release real.

## Pulado/Falhou
- Nada revertido. Limitação conhecida: o cask serve macOS; Linux usa `install.sh`. Os roteiros do modo fake provam
  cenários e verificadores, não a qualidade do modelo; só o modo live mede isso.
