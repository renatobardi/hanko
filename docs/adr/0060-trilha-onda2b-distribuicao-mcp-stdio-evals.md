# ADR-0060 — Onda 2B: instalação (install.sh + Homebrew), `hanko mcp` em stdio e evals
Data: 2026-10-22 · Status: aceita · Complementa: ADR-0036

## Contexto
Três itens da trilha contínua dependem do mundo externo (GitHub Releases, um tap, clientes MCP que só falam stdio, um modelo
pago). O contrato é o mesmo para os três: interfaces e testes contra fakes, nada publicado e nenhuma chamada paga por
padrão ou no CI de PR.

## Decisão
1. **Instalação (#54).** `scripts/install.sh` (POSIX sh) baixa o `.tar.gz` da release e o `checksums.txt` do goreleaser,
   confere o sha256 e instala em `HANKO_INSTALL_DIR` (padrão `/usr/local/bin` se gravável, senão `~/.local/bin`), sem root.
   A base das releases é configurável (`HANKO_RELEASES_URL`, aceita `file://`), o que permite testar o script inteiro sem
   rede (`scripts/test-install.sh`, no `ci`). O tap Homebrew é um **cask** gerado pelo goreleaser (`homebrew_casks`): o
   bloco `brews` está obsoleto e faz o `goreleaser check` do `release-dry-run` falhar. Consequência: `brew install --cask`
   serve macOS; Linux usa o `install.sh`. O envio ao `renatobardi/homebrew-tap` só acontece com o secret
   `HOMEBREW_TAP_GITHUB_TOKEN` (`skip_upload` por template), então fork e snapshot não quebram.
2. **`hanko mcp` (#52).** Ponte de mensagens JSON-RPC entre stdio e o `/mcp` remoto (`internal/mcpbridge`), usando o SDK
   que o servidor já usa. Não interpreta o protocolo: tools novas do servidor aparecem sem mudar o binário. A credencial é a
   que a CLI já resolve (`--token`, `HANKO_TOKEN` ou o contexto salvo por `hanko login`) e vai como `Authorization: Bearer`.
   Se o stdin acaba (uso com pipe), a ponte espera as respostas das requisições já enviadas antes de sair.
3. **Evals (#58).** `internal/evals` + `cmd/hanko-evals`. Dois pontos de troca: `Model` (roteirizado por padrão, Anthropic
   real com `--live`/`HANKO_EVALS_LIVE=1` + `ANTHROPIC_API_KEY`) e `Harness` (mundo em memória por padrão; `MCPHarness`
   contra um Hanko de teste). Cenários de comportamento (gate antes de produção, delegação, dependência, não vazar
   segredo) e de chaos (run morta, banco fora) usam os mesmos verificadores nos dois modos; cada roteiro tem um controle
   negativo que o verificador tem de reprovar. O modo live recusa rodar em `pull_request`, tem teto de gasto estimado
   (`HANKO_EVALS_MAX_USD`, padrão US$ 1) e marca todo custo como `kind: "test"` no relatório. O chaos de infraestrutura
   (`PGInjector`) derruba um banco de teste de verdade e confere que o pool volta.

## Alternativas descartadas
- Bloco `brews` (fórmula): obsoleto no goreleaser v2 e reprova o `check`.
- Proxy MCP que reimplementa as tools no cliente: duplicaria o servidor e envelheceria a cada tool nova.
- SDK de terceiros para a API do modelo nos evals: uma chamada HTTP basta, sem dependência Go nova.
- Rodar o modelo real no PR: custo e risco de fork.

## Consequências
- Sem migration, sem dependência Go nova, sem rota de API nova. O cenário "pago" só existe quando alguém liga a flag.
- O cask exige criar o repositório `renatobardi/homebrew-tap` e o secret antes da primeira tag para publicar.
- Os evals com roteiro de mentira garantem que cenários e verificadores funcionam; só o modo live mede o modelo.
