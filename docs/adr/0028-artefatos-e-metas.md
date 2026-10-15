# ADR-0028 — Artefatos versionados e metas
Data: 2026-10-15 · Status: aceita

## Contexto
O trabalho das personas acabava em comentários: um relatório, um plano ou um conjunto de dados virava texto colado na
thread, sem versão, sem lugar certo para abrir e sem dono. Do outro lado, não havia como dizer "o que isso deve mover":
projetos agrupam tasks, mas ninguém media se o trabalho andava na direção de um número. O menu mostrava "Artefatos" e
"Metas" como "Em breve". Esta decisão cobre as duas peças, porque elas se encontram na mesma tela de trabalho (a task e a
execução) e na mesma Caixa de entrada.

## Decisão

### A. Artefatos
1. **Modelo.** Duas tabelas (migration 00019): `artifacts` (metadados) e `artifact_blobs` (o `bytea`, 1:1 com a versão),
   para que nenhuma listagem leia o conteúdo. Cada linha de `artifacts` é **uma versão**. A cadeia é
   (empresa, task ou "sem task", nome): o mesmo nome na mesma task cria a versão seguinte. A versão nova é a da ponta da
   cadeia + 1, lida sob `pg_advisory_xact_lock` da cadeia (duas publicações simultâneas não repetem número), e há índice
   único (empresa, task, nome, versão). Publicar de novo o **mesmo conteúdo** (sha256 e tipo iguais aos da ponta da cadeia) não
   cria versão: devolve a existente. Os vínculos com task, run, projeto e persona são opcionais e `ON DELETE SET NULL`
   (apagar a task não apaga o arquivo). Com task e sem projeto explícito, o projeto da task é herdado; com run e sem task,
   a task da run; o ator persona grava `persona_id`. O conteúdo fica no Postgres (ADR-0011): um backup só, sem diretório
   a mais para proteger.
2. **Limites.** Por arquivo, `HANKO_ARTIFACT_MAX_BYTES` (padrão 5 MiB, de 1 a 16 MiB); por empresa,
   `HANKO_ARTIFACT_QUOTA_BYTES` (padrão 512 MiB, soma de todas as versões). Conteúdo vazio é recusado. O nome tem de 1 a
   200 caracteres, sem barras nem caracteres de controle: é um rótulo, nunca um caminho (o conteúdo não toca o disco). O
   tipo vem do cliente quando informado (normalizado) ou da extensão e do conteúdo. A recusa por tamanho ou cota é
   `ArtifactLimitError`, que é erro de validação (422) e carrega os números para a tela explicar.
3. **Entrega segura.** O conteúdo é de terceiros (uma persona, um upload), então nunca é página. Um único pacote,
   `internal/artifactsrv`, entrega os bytes na API e na UI: `X-Content-Type-Options: nosniff`, `Content-Security-Policy:
   default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; sandbox`, `Cross-Origin-Resource-Policy:
   same-origin`, e `Content-Disposition: inline` **só** para `text/plain`, `text/markdown` e imagens raster
   (png, jpeg, gif, webp). Todo o resto, inclusive HTML, SVG e PDF, sai como `attachment`. O nome do arquivo é codificado
   por `mime.FormatMediaType` (sem injeção de cabeçalho). Não existia CSP no app; ela passa a valer só para o conteúdo
   de artefato (uma CSP para as páginas da UI é outra decisão).
4. **Prévia e diff na tela.** Markdown passa pelo renderizador interno (`internal/ui/md`, que escapa HTML cru); texto vai
   num `<pre>` escapado (até 256 KiB, só UTF-8 válido, com aviso de corte); imagem raster num `<img>` que aponta para a
   rota de conteúdo; o resto não tem prévia, só download. O fonte de um SVG é texto e nunca é desenhado. O diff entre
   versões (`internal/textdiff`, menor subsequência comum por linha, sem dependência nova, com teto de células que
   degrada para "tudo removido, tudo adicionado") vale para texto UTF-8 de até 512 KiB por lado; a tela mostra 3 linhas de
   contexto e até 3000 linhas.
5. **Papéis (ADR-0021).** `viewer` lê; `member` publica; `admin` exclui (a cadeia inteira, de forma definitiva). Personas
   (run token) publicam sem papel, **só na própria task e execução**. Fatos: `artifact.created` e `artifact.deleted`,
   na mesma transação.
6. **Interfaces.** API `…/artifacts` (envio em JSON com `content` em base64, lista só com a versão mais nova, histórico,
   conteúdo bruto, `diff`, `artifact-usage`); CLI `hanko artifact put|list|get|diff|rm|usage`; MCP `hanko_artifact_put`
   (texto em `content`, binário em `content_base64`; **sem** tools de leitura: o agente já está no workspace, quem lê é o
   humano); UI `/artefatos` (filtros projeto, task, persona, tipo e busca pelo nome; envio multipart com o corpo limitado
   por `http.MaxBytesReader`; detalhe com prévia, versões e diff; download). As páginas da task, da execução (todas as
   versões que ela publicou) e do projeto listam seus artefatos.
7. **Exportar/importar (ADR-0022).** O formato continua `hanko.company.v1`: o campo `artifacts` é **opcional** e só sai
   com `--with-artifacts` (API `with_artifacts=true`); arquivos antigos importam como sempre. A exportação escreve em
   fluxo, um artefato por vez, com teto de 20 MiB no arquivo (para caber nos 32 MiB que a importação aceita). A importação
   valida nomes, versões contíguas, sha256, referências a task/run/projeto/persona e a cota **antes** de gravar.

### B. Metas
1. **Modelo.** `goals` (slug único na empresa, título, descrição em Markdown, métrica, unidade, valor inicial, alvo e
   atual, `due_date` sem hora, status `active|achieved|abandoned`, dona persona ou, sem dona, o time humano, projeto
   opcional), `goal_updates` (histórico imutável: o valor lido, a nota e quem registrou) e `tasks.goal_id`
   (`ON DELETE SET NULL`). Alvo e inicial têm de diferir; o alvo pode ser **menor** (meta de redução). Os valores são
   finitos (NaN e Inf são recusados).
2. **Progresso** = (atual − inicial) / (alvo − inicial), limitado a [0, 1]. Abaixo do inicial é 0; passar do alvo é 1.
3. **Atingida.** Registrar um valor que alcança o alvo (na direção da meta) muda o status para `achieved` na mesma
   transação, grava `achieved_at` e o fato `goal.achieved`. A Caixa de entrada mostra a meta por 7 dias como item
   **informativo** (ninguém precisa agir, então não entra em `Inbox.Count()`). Meta atingida ou abandonada não recebe
   progresso nem task nova; reabrir é trocar o status para `active` (recusado enquanto o valor atual já alcança o alvo,
   para não "atingir" de novo sem querer). `achieved` não se escolhe, só acontece.
4. **Tasks.** Uma task serve a **uma** meta ativa (`goal`/`clear_goal` na API, `--goal` e `hanko task goal` na CLI,
   seleção na task). A página da meta mostra as tasks ligadas e a contagem por status.
5. **Agentes.** O briefing da run ganha a seção curta "Metas ativas" (progresso, valores, prazo, e a meta da task
   marcada) e o protocolo manda registrar o valor medido. MCP: `hanko_goal_list` e `hanko_goal_update` (o valor atual, não
   o incremento). A persona só deve registrar o que mediu de fato.
6. **Papéis.** `viewer` lê; `member` (e a persona) registra progresso e liga tasks; `admin` cria, edita, abandona e
   reabre.
7. **Interfaces.** API `…/goals` (CRUD e `POST …/progress`), CLI `hanko goal create|list|show|update|progress`, UI
   `/metas` (abas ativas/atingidas/abandonadas/todas, barra de progresso, prazo e atraso, filtro por projeto) e a página da
   meta com o histórico em **SVG desenhado no servidor** (sem script), formulário de progresso, tasks ligadas, e o
   quadro "Metas" na página do projeto. Os itens "Metas" e "Artefatos" saem de "Em breve" em `nav.go`.

## Consequências
- Guardar arquivos em `bytea` simplifica o backup e o isolamento por empresa, mas faz o dump crescer com os artefatos;
  por isso o teto por arquivo, a cota e a separação das tabelas. Versões não compartilham blob (sem deduplicação entre
  versões diferentes). Uma instalação com muitos binários grandes deve subir a cota com consciência do tamanho do backup.
- Excluir é definitivo e apaga a cadeia toda (sem lixeira). A exclusão é de admin e fica na atividade.
- Não há busca no conteúdo, só no nome. O diff é só de texto.
- Servir tudo que não é texto puro, markdown ou imagem raster como download tira do navegador o risco de HTML e SVG
  vindos de um agente, ao custo de HTML e PDF não terem prévia.
- O export com artefatos tem teto de 20 MiB no arquivo; acima disso, a exportação recusa e o operador deve exportar sem
  artefatos. **Metas não são exportadas nem importadas nesta versão**: uma empresa importada chega sem metas, e as tasks
  importadas ficam sem `goal_id`.
- Sem tools MCP de leitura de artefato e sem `goal` em `hanko_task_create`: o agente publica e mede; ligar uma task a uma
  meta é decisão humana (UI, CLI ou API).
- O cliente Go `ExportCompany` passou a receber `ExportOpts{Runs, Logs, Artifacts}`.
- O progresso usa `double precision`: metas de dinheiro com centavos exigem cuidado de arredondamento no consumidor; a
  tela mostra até duas casas decimais.
- Run de chat não tem task (ADR-0027): um artefato publicado nela fica só com a run (e o projeto, se informado); a
  exportação grava `task: 0` e a importação não inventa task. Com token de run, `hanko_artifact_put` aceita `task` só
  se for a task da execução.
- Os fatos `artifact.created` e `goal.achieved` já saem na atividade, mas **não** são eventos assináveis dos webhooks
  (ADR-0029) nesta versão. Assiná-los exige acrescentar `artifact.published` e `goal.reached` à lista de eventos e ao
  mapeamento de verbos em `webhookEventFor`: fica como evolução futura.

## Adendo: correções da revisão independente
- **Cota sob concorrência.** A trava por cadeia só serializa a numeração; a cota é por empresa. `PutArtifact` agora
  toma antes `pg_advisory_xact_lock(hashtextextended('artifacts:'||company_id, 0))` e só então a trava da cadeia (ordem
  fixa, sem ciclo: a exclusão toma só a da cadeia). O uso é lido e consumido sob a trava da empresa, então uploads
  simultâneos de nomes diferentes não passam da cota. O custo é serializar as gravações de artefatos de uma mesma empresa.
- **Briefing.** `ListGoalsForBriefing` ordena a meta da própria task primeiro; o corte em 5 nunca a descarta.
- **Corpo do MCP.** O transporte MCP limitava o corpo a 4 MiB (cerca de 3 MiB de arquivo em base64), abaixo do limite
  configurado. O corpo aceito agora é `max(4 MiB, HANKO_ARTIFACT_MAX_BYTES x 4/3 + 64 KiB)`. Vale para todas as chamadas
  MCP autenticadas, não só para `hanko_artifact_put`. O caminho de arquivos grandes é `content_base64`: texto em
  `content` pode crescer mais no JSON (escapes).
- **Run de chat na API.** O run token de chat não tem task: a API aceita só `run_id` (o do token) e recusa `task`.
- **Vínculo com a task.** `artifacts.task_id` passou a `ON DELETE CASCADE` (migration 00025): com `SET NULL` as versões
  se fundiam à cadeia "sem task" do mesmo nome e podiam violar o índice único de versão. Run, projeto e persona seguem
  `SET NULL`. Substitui o "apagar a task não apaga o arquivo" da Decisão.
- **Nome.** Caracteres Unicode de formatação (categoria Cf: bidi como U+202E, largura zero) são recusados na
  publicação e na importação, para que o nome não disfarce a extensão.
- **Corpo da API.** O limite do corpo da rota de envio vem do `HANKO_ARTIFACT_MAX_BYTES` configurado (base64 x 4/3 +
  64 KiB) no registro da rota, não do máximo absoluto de 16 MiB.
- **Imagens no Markdown.** O render de artefato não carrega imagem de URL externa (vira link com `rel="noopener
  noreferrer nofollow"`); imagem de caminho relativo sai com `loading="lazy"` e `referrerpolicy="no-referrer"`. Os demais
  usos de Markdown (comentários, descrições) não mudam.
