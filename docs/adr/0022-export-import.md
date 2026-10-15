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
   transitórios de persona (`running`, `error`, `awaiting_gate`) voltam como `idle`. Os logs saem **em fluxo**: a
   configuração e as execuções (sem eventos) são montadas em memória, mas os eventos são lidos por execução, em páginas
   de 1000 (keyset por `seq`), e escritos direto na resposta; o log inteiro da empresa nunca fica na memória. Um erro
   no meio da escrita (o status 200 já foi enviado) deixa o arquivo truncado, isto é, JSON inválido, que o cliente
   detecta ao ler.
5. **Exportação consistente.** Uma transação `REPEATABLE READ` somente leitura: o arquivo é um instante único (a
   transação fica aberta enquanto a resposta é escrita).
6. **Importação cria uma empresa NOVA**, nunca mescla: `--slug` (ou o do arquivo; colisão é 409, nada é gravado).
   Tudo numa transação: o arquivo é validado por inteiro antes de qualquer escrita (formato, referências, duplicatas,
   ciclos no organograma e entre tasks pai/filha, enums, cron e fuso, agentes conhecidos, caminho de workspace aceito
   pela instalação) e qualquer falha no banco desfaz tudo. Os ids são novos; `created_at` é preservado, e os ids são
   gerados na ordem do arquivo para que a ordenação (created_at, id) sobreviva à ida e volta. A numeração das tasks é
   mantida e a próxima continua de onde parou. Números de task vão de 1 a 1e9; custos e tokens de execuções não podem
   ser negativos e o início de um período de assinatura não pode ser zero.
   **A empresa importada entra PAUSADA por padrão** (personas pausadas, fluxos desligados): um arquivo de terceiros
   não faz nada sozinho. `--active` na CLI (`paused=false` na API) mantém o que o arquivo traz ligado.
7. **Atividade.** A importação emite `company.imported` (humano) na empresa nova; não replica a atividade antiga.
8. **Autorização** (ADR-0021). Exportar exige admin (as instruções e o histórico não são para qualquer membro).
   Importar cria uma empresa, então vale a regra de criar empresa: `users.role` owner ou admin; quem importa vira
   owner da nova. Run tokens de persona recebem 403 nos dois endpoints. No modo trusted não há restrição.
9. **API e CLI.** `GET /api/v1/companies/{c}/export?with_runs&with_logs` e `POST /api/v1/companies/import?slug=&paused=`
   (`paused` vale `true` se omitido). A importação é um handler HTTP direto, fora do huma: o corpo (até **64 MiB**; o
   padrão de 1 MiB não comporta logs) é decodificado em fluxo por `json.Decoder`, sem cópia nem validação de esquema
   em memória (o service valida tudo), com prazo de leitura de 5 min (o padrão do huma, 5 s, devolvia 408 em upload
   grande). Acima do limite é 413; corpo inválido é 400 e **a resposta nunca ecoa o corpo**. No OpenAPI o arquivo é um
   objeto livre com `format` e `company` obrigatórios (os tipos do arquivo colidiriam com os da API; quem valida é o
   service). CLI: `hanko export [-o arq] [--with-runs] [--with-logs]` e `hanko import <arq|-> [--slug x] [--active]`.
10. **O arquivo não é anônimo.** Textos livres (instruções, descrições, títulos, comentários, resumos e erros de
    execução, logs) passam por `adapter.Redact`, que mascara credenciais conhecidas (Bearer, JWT, chaves de API, URL de
    banco com senha) como `[REDACTED]`. Isso não torna o arquivo anônimo: ele contém nomes de pessoas e personas, o
    texto de instruções e tasks, URLs de repositório e **caminhos de workspace** da instalação de origem. Revise-o
    antes de compartilhar fora da organização.

## Consequências
- O round trip export → import → export é igual, exceto `exported_at` e o slug (testado, com e sem execuções/logs).
- Memória do ai-memory, gates, alertas, orçamentos consumidos e fires de fluxo não viajam: são estado de operação.
- Mudança incompatível no formato exige `hanko.company.v2`; o importador recusa formatos que não conhece.
