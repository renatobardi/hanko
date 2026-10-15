# ADR-0022 — Exportar e importar uma empresa
Data: 2026-10-15 · Status: aceita

## Contexto
Não havia como levar um time de personas (organograma, instruções, revisores, assinaturas, fluxos) de uma instalação
para outra, nem guardar um "modelo" de empresa. Um dump do banco serve para backup, mas carrega ids, segredos e dados
de outras empresas; o que se quer é um arquivo legível, versionável e seguro de compartilhar.

## Decisão
1. **Formato.** Um único JSON `"format": "hanko.company.v1"` com empresa, workspaces, projetos, assinaturas (e períodos),
   personas, tasks com comentários, fluxos e, opcionalmente, execuções. Os tipos estão em `internal/export`.
2. **Sem ids.** Tudo é referenciado por slug, nome da assinatura ou número da task (`reports_to`, `reviewer`,
   `subscription`, `persona`, `parent`...). O arquivo é estável, diferenciável e editável à mão. Execuções têm uma chave
   local (`r1`, `r2`...) só para os comentários que apontam para elas.
3. **Sem segredos.** `adapter_config` passa por `export.ScrubConfig` na exportação E na importação (um arquivo editado
   à mão também não introduz credencial): remove chaves sensíveis (token, senha, api key...), flags de linha de comando
   com credencial e valores que pareçam chaves. O que é removido some por inteiro (nada de "[REDACTED]"). Run tokens,
   sessões, API keys, membros e convites nunca entram. O ledger de custos globais (preços) não é da empresa e fica fora.
4. **Execuções são opcionais.** `--with-runs` inclui as encerradas (com custo, tokens, nome amigável); `--with-logs`
   inclui também os eventos. Por padrão o arquivo é só configuração. Execuções ativas nunca são exportadas, e estados
   transitórios de persona (`running`, `error`, `awaiting_gate`) voltam como `idle`.
5. **Exportação consistente.** Uma transação `REPEATABLE READ` somente leitura: o arquivo é um instante único.
6. **Importação cria uma empresa NOVA**, nunca mescla: `--slug` (ou o do arquivo; colisão é 409, nada é gravado).
   Tudo numa transação: o arquivo é validado por inteiro antes de qualquer escrita (formato, referências, duplicatas,
   ciclos no organograma e entre tasks pai/filha, enums, cron e fuso, agentes conhecidos, caminho de workspace aceito
   pela instalação) e qualquer falha no banco desfaz tudo. Os ids são novos; `created_at` é preservado, e os ids são
   gerados na ordem do arquivo para que a ordenação (created_at, id) sobreviva à ida e volta. A numeração das tasks é
   mantida e a próxima continua de onde parou. `--paused` importa personas pausadas e fluxos desligados.
7. **Atividade.** A importação emite `company.imported` (humano) na empresa nova; não replica a atividade antiga.
8. **Autorização** (ADR-0021). Exportar exige admin (as instruções e o histórico não são para qualquer membro).
   Importar cria uma empresa, então vale a regra de criar empresa: `users.role` owner ou admin; quem importa vira
   owner da nova. Run tokens de persona recebem 403 nos dois endpoints. No modo trusted não há restrição.
9. **API e CLI.** `GET /api/v1/companies/{c}/export?with_runs&with_logs` e `POST /api/v1/companies/import?slug=&paused=`
   (corpo até 256 MiB; o padrão de 1 MiB não comporta logs). No OpenAPI o arquivo é um objeto livre com `format` e
   `company` obrigatórios (os tipos do arquivo colidiriam com os da API; quem valida é o service). CLI: `hanko export
   [-o arq] [--with-runs] [--with-logs]` e `hanko import <arq|-> [--slug x] [--paused]`.

## Consequências
- O round trip export → import → export é igual, exceto `exported_at` e o slug (testado, com e sem execuções/logs).
- Memória do ai-memory, gates, alertas, orçamentos consumidos e fires de fluxo não viajam: são estado de operação.
- Mudança incompatível no formato exige `hanko.company.v2`; o importador recusa formatos que não conhece.
