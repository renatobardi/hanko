# ADR-0052 — Revisões append-only, anexos, rascunhos e o chat (`/new`, anexos, projeto e meta)
Data: 2026-10-17 · Status: aceita

## Contexto
Quatro lacunas pequenas, uma base comum.
- **As instruções da persona são sobrescritas.** `personas.instructions` muda no lugar; quem trocou, por quê e o que mudou
  se perde, e uma run não diz com que texto rodou. As skills já resolveram isto (ADR-0035: `skill_versions`, restaurar cria
  versão nova, fixação por persona); falta o mesmo para a instrução, e uma forma de **não duplicar** o padrão quando fluxos
  e gates ganharem histórico.
- **Anexo é só artefato de agente.** O humano não consegue anexar um arquivo a uma task nem a um comentário, e o agente
  não recebe a lista no briefing.
- **Rascunho vive no navegador.** Um comentário meio escrito some na troca de dispositivo.
- **Chat.** Não há como começar do zero sem abrir outra conversa, não há anexo na conversa e o agente não cria projeto nem
  meta.

## Decisão

### A. Revisões append-only (#7)
1. **Padrão (a base reusável).** Uma tabela de revisões por assunto, com as mesmas colunas: `(company_id, <assunto>_id,
   revision ≥ 1, body, sha256, author_*, actor_*, reason, origin, created_at)`, único por (assunto, revision), e o gatilho
   genérico `hanko_revision_immutable()` (migration 00040) que **recusa `UPDATE`** em qualquer tabela que o instale. `author`
   é quem escreveu o texto (um humano, ou a persona que propôs); `actor` é quem o aplicou (o humano que editou, restaurou
   ou aprovou o gate). `reason` é livre (até 500 caracteres). `sha256` é o do corpo em UTF-8. A tabela **não tem chave
   estrangeira com `SET NULL`** (isso seria um `UPDATE` do banco): referências a outras linhas são só ids. Apagar o assunto
   apaga as revisões por cascata, como em `skill_versions`. No código, `domain.Revision` (metadados + `RevisionSHA`) e o
   diff por linha (`internal/textdiff`) são compartilhados; `skill_versions` mantém o formato dele (tem nome e descrição, e
   `revision` é o `skills.version`) e **adota só o que é comum**: o hash e o diff passam a vir do mesmo lugar. Fluxos e
   gates devem criar a tabela deles com este formato e instalar o gatilho, sem inventar outro.
2. **`persona_instruction_revisions`.** Toda escrita de `personas.instructions` (contratar, editar, restaurar, aprovar uma
   proposta, importar uma empresa) grava a revisão **na mesma transação**, com o fato na atividade
   (`persona.instructions_changed`). `personas.instruction_revision` guarda o número vigente (lido junto da linha da
   persona, então o texto e o número da run nunca se separam). Restaurar cria uma revisão **nova** com o texto antigo
   (`origin = restore`, `restored_from`); restaurar o texto que já vale não faz nada. Backfill: o texto atual de cada persona
   vira a revisão 1 (`origin = backfill`, autor `system`).
3. **A run grava a revisão usada.** `runs.instruction_revision` e `runs.instruction_sha256` são preenchidos quando a run
   começa (o executor repassa a revisão do mesmo `RunContext` de onde veio o texto do prompt). A tela da run e a API mostram
   "instruções r7 · sha" com link para o diff. Runs antigas ficam sem o dado (nulo), não inventado.
4. **Persona propõe via gate.** Gate novo `kind = instruction` (a lista completa da constraint passa a ser
   `approval, task_review, hire, memory, tool, instruction`). A persona chama `hanko_persona_instruction_propose` (MCP,
   run token): o texto proposto, o motivo e a revisão-base ficam em `persona_instruction_proposals` (1:1 com o gate) e o
   gate mostra o diff contra a revisão-base. **Não bloqueia** a persona nem a task (é uma sugestão). Só a própria persona
   propõe para si. Aprovar (admin) aplica a revisão (`origin = proposal`, autor = a persona, ator = o humano) **se a
   revisão-base ainda é a vigente**; senão a decisão falha com conflito (alguém editou no meio: rejeite e peça de novo).
   Rejeitar ou pedir mudanças não toca na persona. Um humano nunca precisa de gate para editar.
5. **Papéis.** `viewer` lê o histórico e o diff; `admin` edita, restaura e decide o gate (como hoje `UpdatePersona`).
6. **Superfícies.** API `…/personas/{p}/instructions/revisions` (lista), `…/{n}` (corpo), `…/diff?from=&to=`, `POST
   …/{n}/restore`; o `PATCH` da persona aceita `instructions_reason`; CLI `hanko persona history|diff|restore`; UI: aba
   "Instruções" na persona (histórico, diff, restaurar, motivo no formulário) e a revisão na página da run.

### B. Anexos (#5)
1. **Anexo é um artefato** com `artifacts.kind = 'attachment'` (padrão `artifact`, migration 00041), mais
   `comment_id` (opcional, `ON DELETE CASCADE`: apagar o comentário apaga o anexo) e `conversation_id` (anexo do chat).
   Mesmo `artifact_blobs`/BlobStore (ADR-0051), mesma cota e mesmo limite por arquivo (ADR-0028), mesma entrega segura
   (`internal/artifactsrv`: `nosniff`, CSP `default-src 'none'; sandbox`, `inline` só para texto puro, markdown e raster).
2. **Sem cadeia de versões.** Um anexo é sempre a versão 1: nome repetido na mesma task (ou conversa) recebe sufixo
   (`foto.png`, `foto (2).png`), em vez de virar versão do anterior. O índice único da cadeia passa a incluir `kind` e
   `conversation_id`, e as consultas de cadeia do artefato filtram `kind = 'artifact'`: um anexo e um artefato de mesmo
   nome não se misturam. As listagens de artefatos (tela, API, export) mostram só `artifact` por padrão; `kind=attachment`
   (ou `all`) lista os anexos.
3. **Quem anexa.** `member` (e a persona, na própria task/execução; o run token de chat, na própria conversa). Fato
   `attachment.added` na atividade. `admin` exclui o anexo (um só, sem cadeia).
4. **Briefing.** O prompt da run lista os anexos da task (nome, tipo, tamanho, quem e quando, e a que comentário
   pertencem), no máximo 30; o agente lê o conteúdo com `hanko_attachment_get` (texto até 256 KiB; binário volta em base64
   até 1 MiB; acima disso, só os metadados). `hanko_attachment_list` lista; `hanko_attachment_put` anexa (mesmo
   `hanko_artifact_put` com `kind=attachment`). No chat, a run recebe os anexos das mensagens novas.
5. **Upload.** UI: soltar arquivos (arrastar e soltar) na task ou no formulário de comentário/mensagem e escolher no seletor;
   envio multipart com corpo limitado por `http.MaxBytesReader`; sem JavaScript novo além de um trecho pequeno no
   `hanko.js` que liga a zona de soltar ao `input[type=file]`. API `POST …/tasks/{t}/attachments` (JSON com base64, como
   o artefato) e `GET …/attachments`; CLI `hanko attach <task> <arquivo>… [--comment id]` e `hanko attachment list|get|rm`.
6. **Export.** Anexos **não** entram no `hanko.company.v1` nesta versão (o formato não muda; a exportação com artefatos
   filtra `kind = artifact`).

### C. Rascunhos no servidor (#6)
1. `drafts (company_id, user_id, target_type task|conversation, target_id, body, updated_at)`, único por
   (empresa, usuário, tipo, alvo) — migration 00042. Só texto, até 100 000 caracteres (o limite do comentário/mensagem). Sem
   histórico: um rascunho é o último estado.
2. **Por usuário humano.** Quem não tem identidade de usuário (modo trusted/CLI sem login) usa o usuário local
   `local`; persona não tem rascunho. Ler e gravar exigem `member`; ninguém lê o rascunho de outro.
3. **Limpa ao enviar.** Enviar o comentário (ou a mensagem) apaga o rascunho na **mesma transação** do envio; apagar também
   é um `DELETE` explícito. Gravar um corpo vazio apaga. Rascunho sem alvo existente é recusado (404). Apagar a task ou a
   conversa apaga os rascunhos (cascata).
4. **Autosave por htmx.** O `textarea` de comentário e de mensagem faz `hx-put` com `hx-trigger="keyup changed delay:1s,
   blur"`, sem recarregar; ao abrir a página o servidor devolve o rascunho dentro do campo (por isso funciona em outro
   dispositivo). Não é fato de atividade (é ruído de edição, não uma escrita de domínio); `drafts` não entra no export.
5. API `GET|PUT|DELETE …/drafts/{target_type}/{target_id}`.

### D. Chat (#11)
1. **`/new`.** Uma mensagem que é exatamente `/new` (ou começa com `/new ` + texto) insere um **marcador de contexto**:
   `conversation_messages.marker = 'context_reset'` (migration 00043), mensagem de sistema sem run. O contexto de uma run
   passa a ser só o que vem **depois do último marcador** (histórico do prompt) e nenhuma run anterior ao marcador
   retoma sessão (`session_ref` é ignorado, não apagado: o log da run continua inteiro). Com texto depois de `/new`, o
   texto é enviado normalmente após o marcador. Vale para a conversa direta e para a sala (ADR-0033: o marcador é da
   conversa, vale para todas as personas dela; a sessão por persona também recomeça). A tela mostra uma linha
   "contexto reiniciado".
2. **Anexos no chat** seguem o B, com `conversation_id` e, quando enviados com a mensagem, `message_id`
   (`artifacts.message_id`, `ON DELETE CASCADE`). A run de chat recebe a lista dos anexos das mensagens que ela recolheu.
3. **Tools MCP** `hanko_project_create` (nome, slug, descrição, workspace, orçamento) e `hanko_goal_create` (título, métrica,
   inicial, alvo, prazo, projeto, dona). Valem para qualquer run com token (inclusive a de chat), via os métodos
   `CreateProject`/`CreateGoal` do service, com as mesmas validações e fatos (`project.created`, `goal.created`); um projeto
   ou meta criado por uma persona carrega `created_by = persona`. Idempotência por `event_id`/slug existente: repetir devolve
   o recurso existente em vez de falhar.

## Consequências
- Texto antigo de instrução fica para sempre no banco (somem só com a persona). Instruções não têm segredo por regra do
  produto; quem as usou para guardar um segredo deve movê-lo para o cofre (`${secret:X}`, ADR-0050) e saber que a revisão
  anterior mantém o texto.
- O gatilho de imutabilidade é rede de segurança, não autorização: o service continua sendo a única via de escrita.
- Anexos contam na cota dos artefatos e são apagados com a task (cascata) ou o comentário.
- Rascunho é por usuário e por alvo; trocar de dispositivo funciona porque o servidor devolve o texto ao abrir a página.
  O que o usuário digitou há menos de um segundo do fechamento da aba pode não ter sido salvo.
- `/new` não apaga nada: é um marcador. O histórico completo continua visível e exportável.
- Sala multi-persona (ADR-0033): nada muda no despacho; só o corte de contexto e de sessão.
- Dependência Go nova: nenhuma.
