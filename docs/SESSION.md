# Diagnóstico da produção (hanko-prd) · sessão `bold-ptolemy`
Data: 2026-11-01

## Feito
- `scripts/hanko-diag.sh` rodou completo no LXC `hanko-prd` (v791fe025). Servidor saudável (doctor 8 OK, só WARN de boot). Os erros estão no log das runs, não no do servidor.
- Causa 1 ("falta de acesso" ao GitHub): o gateway de tools (ADR-0053) chama o MCP remoto do GitHub só com `arguments`; o upstream exige os parâmetros marcados `x-mcp-header` no input schema como cabeçalho `Mcp-Param-<nome>` e recusa com `-32020 header mismatch: missing Mcp-Param-owner`. `internal/mcpupstream/mcpupstream.go:190` (`CallTool`) não lê o schema nem envia cabeçalho por chamada; o catálogo já guarda o `InputSchema` (`internal/service/tools_gateway.go:147`). Só `search_*` funcionam porque não têm `owner`.
- Causa 2 (Bash negada): persona sem política de tools nativa → `NativeToolCheck` devolve `none` (`internal/service/tools_policy.go:369-390`), o hook não responde e vale o `--permission-mode acceptEdits` do claude headless, que recusa `git log`, `which gh` etc. ("requires approval"). Comportamento previsto no ADR-0053 §"sem política", mas sem aviso ao operador. `gh` nem existe na imagem `Dockerfile.agents`.
- Causa 3 (menores): `demo-arquivo` → `${secret:demo-kube}` inexistente (WARN a cada boot); `demo-cofre` fora da coleção do segredo; `tester-mutation` com alias de modelo inválido `claude-haiku` (readiness nunca testada); WARN "falha de exportação OpenTelemetry" carregando erro de banco (`count alerts`); 6 dos 7 workspaces sem Git ligado (pastas vazias), só `kubo` com espelho `ready`; chat usa só o workspace da persona (`conversations.go:562`), Delivery Manager não tem workspace.
- CI: `TestExternalPaidFollowsMonthlyHistory` falhava em todo dia 1 (ontem cai no mês anterior, que a assinatura recém-criada não acumula); o período aberto passa a começar em ontem. Reproduzido e corrigido com Postgres 16 local.
- Script: `hk` repassa `HANKO_TOKEN`/`HANKO_COMPANY`; JSON indentado lido por `jfield`; seção 9 com espelhos e `workspace git show`. PR #183.

## Travado
- Nada. Três API keys ficaram expostas no chat da sessão; Bardi revoga.

## Próximo passo
- Correções em ordem: (1) gateway envia `Mcp-Param-*` a partir do `x-mcp-header` do schema, com teste; (2) decidir política nativa padrão (perfil "dev" com Bash liberada) ou aviso na run quando `none` recusa; (3) rótulo correto do WARN de OTel; (4) alerta/readiness para `${secret:}` inexistente e alias de modelo inválido; (5) `hanko workspace git enable` nos outros workspaces ou aviso de pasta vazia; (6) chat usar workspace do projeto quando a persona não tem.
