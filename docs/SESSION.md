# Assessment de log e acesso ao workspace · sessão `bold-ptolemy`
Data: 2026-10-31

## Feito
- Assessment estático (sem os logs do Bardi) das causas de "erros no log" e "persona sem acesso" num projeto em workspace. Três candidatas: fallback silencioso para diretório temporário quando o caminho não existe ou está fora da raiz (`internal/heartbeat/workspace_root.go`, `executor.go:817-833`), hook `pretooluse` que nega toda tool quando não alcança `HANKO_AGENT_URL` (`internal/cli/toolpolicy.go:276`), e ruído do `ai-memory` padrão fora do ar mais OTLP sem collector.
- `scripts/hanko-diag.sh`: coleta só leitura e mascarada (modo, env, doctor, harness, WARN/ERROR agrupados, workspaces com permissões, readiness, runs com falha, alcance da API). Nota no RUNBOOK §4 e fragmento em `changelog.d/`.

## Travado
- Diagnóstico final depende da saída do script rodado na instalação do Bardi.

## Próximo passo
- Bardi roda `bash scripts/hanko-diag.sh` e cola o `hanko-diag-<data>.txt`. Com isso, priorizar: falha explícita (ou WARN + badge) no fallback para temporário; validar existência e permissão da pasta no cadastro do workspace; alinhar `dirExists` da UI com o executor; ampliar `doctor` (raiz, permissões, outbox de memória); rever `ai-memory` como provider padrão.
