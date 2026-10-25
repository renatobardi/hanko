# ADR-0076 — Ingestão OTLP de agente externo: listener na tailnet, escopo `ingest`, protobuf e replay
Data: 2026-10-25 · Status: proposta (aguarda aceite do Bardi) · Issue: #66 (v0.8) · Usa: ADR-0075

## Contexto
O agente externo (ADR-0075) chega ao Hanko pela telemetria OTLP que o collector do oute hoje manda ao agent-studio.
O contrato atual, que o corte precisa manter para não perder dado (`config/otel/agent-studio.yaml:1-12,33-44@845764f`
do oute-agent): OTLP/HTTP com gzip, fila em disco por exporter, retentativa sem prazo, 2xx só depois do commit e 503
retentável quando o servidor está cheio. O corpo vai até 64 MB depois de descomprimir (`app.py:52@845764f`). Os
exporters mandam JSON e o agent-studio só lê JSON (`otlp.py:8@845764f`). O bucket `oute-observability` guarda tudo em
`otlp_json` (`config/otel/collector.yaml:109,132,152@845764f`) e é a fonte do replay, que hoje roda no lado do oute: o
`oute studio replay` lê o bucket com o `rclone` só leitura e reenvia cada objeto à ingestão
(`scripts/oute:387-390@845764f`). A dedupe usa `s:<trace>:<span>` nos spans e um hash do registro em JSON canônico nos
logs e metrics (`otlp.py:69,132-134,216,278@845764f`).

O Hanko tem API keys com escopo `full` ou `read` (ADR-0057 item 14; `internal/domain/ciclo_de_vida.go:24-35`). O
`hanko-prd` roda num LXC atrás do nginx do host; o vhost público também escuta no IP da tailnet `100.66.254.24:443`
(`docs/RUNBOOK.md:38-40`). Decisões 5, 10 e 11 da #66 e os blocos 1 e 2 e o ponto extra do gate da #67
(`docs/research/2026-10-inventario-agent-studio.md` §9.2 e §9.3). As escolhas abaixo são do Bardi, em 2026-10-25.

## Decisão
1. **Listener próprio de ingestão.** O Hanko abre um segundo listener (`HANKO_INGEST_ADDR`), separado do público. Ele
   atende só as rotas de ingestão, replay e prontidão de ingestão, e só aceita a chave `ingest`. O listener público não
   tem essas rotas. Sem `HANKO_INGEST_ADDR`, a ingestão fica desligada.
2. **Só na tailnet.** Um vhost próprio no nginx do host (`ingest.hanko.oute.pro`) escuta só no IP da tailnet e repassa
   ao listener de ingestão. São duas barreiras independentes, rede e chave. A mudança no nginx segue o fluxo do repo
   `lab`.
3. **Escopo `ingest`.** `api_keys.scope` ganha `ingest`: só grava telemetria, não lê nada, não serve na API pública nem
   em `/mcp`. **Uma chave por host** (hoje `oute-server` e `oute-mac`), todas da empresa do agente externo. O fato
   guarda a chave que o trouxe, além do `host_name` que ele declara; nenhum fato é recusado por divergência entre os
   dois. A chave nunca chega ao container `agent` do oute (decisão 5 da #66).
4. **Formato.** `POST /v1/traces|logs|metrics` aceita **só protobuf** (`application/x-protobuf`), com gzip. No corte o
   collector passa a mandar protobuf; o ensaio (decisão 3 da #66) confere a fila em disco pendente, a dedupe e a volta.
   Se o ensaio falhar no formato, o corte espera a correção. A volta ao agent-studio reverte endpoint e formato juntos.
5. **Replay do bucket.** `POST /v1/replay/{traces|logs|metrics}`, no mesmo listener e com a chave `ingest`, aceita o
   objeto do bucket como ele é (`otlp_json`, com ou sem gzip). É a única entrada de JSON. Quem lê o bucket é a fonte (o
   `oute`, com o acesso só leitura que já tem); o Hanko nunca guarda credencial do bucket. Serve ao ensaio e a qualquer
   fonte que tenha o próprio arquivo de telemetria.
6. **Dedupe igual à do agent-studio.** A chave se calcula sobre o registro decodificado, nas mesmas regras de
   `otlp.py:69,132-134,216,278@845764f`, qualquer que seja o formato que chegou. Um teste confere a chave contra fatos
   reais migrados: replay de uma janela já migrada não duplica nada.
7. **Garantia de entrega mantida.** 2xx só depois do commit; 503 com `Retry-After` quando a ingestão está cheia ou a
   gravação falha; 400 só para corpo inválido ou acima de 64 MB descomprimido, que o collector não retenta. A prontidão
   de ingestão substitui o `/readyz` do agent-studio.
8. **Filtro e máscara ficam no collector.** O `filter/codex_noise` continua no collector: os spans internos do Codex
   ficam só no bucket (bloco 1 do gate da #67). O `transform/redact` também continua antes do envio. O histórico
   migrado passa pela mesma máscara antes de entrar; o original fica na cópia final fria (decisão 13 da #66).
9. **Conteúdo completo** (prompts e respostas) é visível só para owner e admin, e cada acesso gera `activity_event`
   (decisão 10 da #66).
10. **Leitura por consumidor.** Cada consumidor do oute (Hanko Desktop, `ops-observe`, `oute-swarm --budget`, plugin
    herdr, `oute-regression`) tem a sua chave `read`. Pessoas entram com login. Os consumidores são reescritos para a
    API do Hanko, sem camada de compatibilidade com `/v1/usage|alerts|tray|prices` (decisão 11 da #66).

## Consequências
- O collector muda endpoint, formato e chave no corte; os três exporters ganham a chave do próprio host.
- Um host novo precisa de uma chave `ingest` nova antes de mandar telemetria.
- O `oute studio replay` passa a enviar para `/v1/replay/*` do Hanko, com a chave `ingest` do host em que roda.
- Não verificado: se o collector do oute-server, que roda num container do host, alcança o IP da tailnet do próprio
  host. Fica para o ensaio, e vale para qualquer desenho com restrição de rede.
- Uma rajada de ingestão disputa banco, mas não conexão HTTP com a tela (o incidente #570 do oute-agent travou a tela do
  agent-studio pela ingestão).

## Alternativas descartadas
- **Mesmo vhost, com `location` restrito à tailnet**: uma barreira só, e a rota existiria no listener público.
- **Só a chave, sem restrição de rede**: contraria a decisão 5 da #66.
- **JSON e protobuf na ingestão**: o Bardi escolheu protobuf como padrão e JSON só para o bucket.
- **Corte em JSON e protobuf depois**: o Bardi preferiu o padrão desde o corte, conferido no ensaio.
- **O Hanko lendo o bucket direto**: o Hanko guardaria acesso ao arquivo do oute e falaria S3/OCI.
- **O oute convertendo o bucket para protobuf antes do replay**: mais código no oute, sem ganho de garantia.
- **Uma chave `ingest` para a empresa toda**: revogar derrubaria todos os hosts e o fato não provaria de onde veio.
- **Recusar fato com `host_name` diferente do host da chave**: contraria "nenhum fato é recusado" (ADR-0075 item 2).
