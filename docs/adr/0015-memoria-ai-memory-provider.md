# ADR-0015 — Memória: ai-memory ligado por padrão, usado pelo Hanko e pelas personas
Data: 2026-10-13 · Status: aceita (rev. 2: default ligado + Hanko como usuário)

## Contexto
Memória entre sessões de um mesmo workspace, compartilhada entre harnesses (Claude, Codex) **e pelo próprio control plane**. ai-memory (Rust, MIT): wiki markdown em git (fonte da verdade) + SQLite (índice FTS5/entidades/links), hooks de ciclo de vida, handoff entre sessões, consolidação por LLM opcional, 18 tools MCP via HTTP, isolamento por projeto. Opções: (A) integrar; (B) reescrever em Go; (C) embutir (inviável, Rust). Escolha: A.

## Decisão
1. **Ligado por padrão.** Compose de dev/stg/prd e `hanko test-drive` sobem `ai-memory` (imagem fixada por digest). `HANKO_MEMORY_PROVIDER=ai-memory` é o default; `none` só por opção explícita.
2. **Personas usam** (via adapter): config MCP + hooks injetados por run; projeto = `workspace`; sessão = `run_id`; `finalize-session` ao fim do run (cobre harness sem SessionEnd).
3. **O Hanko usa** (MCP client, `go-sdk`):
   - **Escreve** fatos do control plane como observações do workspace: task criada/fechada, decisão de gate (com motivo), resultado de run (status, resumo, custo), delegação, bloqueio.
   - **Lê** no despacho: `memory_briefing` + handoff pendente do workspace entram no contexto inicial do run (antes do 1º prompt), independente do harness suportar hook de SessionStart.
   - **Mostra** na UI: busca de memória por workspace; página do run com handoff recebido/gerado.
4. **Degradável, sem perda.** Toda escrita do Hanko para a memória passa por `memory_outbox` (Postgres, mesma transação do fato de domínio) e é entregue por job river com retry e `event_id` determinístico (dedupe). ai-memory fora do ar ⇒ Hanko segue funcionando, runs abrem sem briefing (aviso no run + alerta), outbox drena quando voltar.
5. **Fronteira.** Recall/contexto = ai-memory. Governança (instruções de persona, gates, lições aprovadas, ledger) = Postgres. ai-memory nunca é fonte de verdade de estado do Hanko.
6. v0.2: consolidação por LLM apontando para o gateway do Hanko (custo no ledger por workspace); permissões de memória por workspace; pending-writes do ai-memory + gate humano.

## Consequências
Revisa ADR-0011: Postgres segue a **única dependência obrigatória**; ai-memory é dependência **default e degradável**. Mais um container em todo ambiente (SQLite + git em volume próprio, backup junto com o pg_dump). Pin por digest; teste de contrato MCP no CI contra a versão fixada. Hooks em `claude -p` precisam de verificação medida. Entra no M2; `memory_outbox` já no schema do M1.

## Adendo (2026-10-15) — itens da v0.2 entregues (ADR-0030)
O item 6 da decisão foi implementado pelo [ADR-0030](0030-consolidacao-de-memoria-governada.md), com uma diferença de rota: a consolidação **não** aponta o LLM do ai-memory para um gateway do Hanko. Ela é uma **run de sistema** de uma persona curadora (gatilho `memory`), então o custo entra no ledger, no orçamento e nos alertas como qualquer run. Escritas que removem, fundem ou reescrevem conteúdo viram propostas com **gate humano** (`memory_proposals`, gate `memory` no Inbox) e só são aplicadas no ai-memory depois da aprovação, por job com retry. As **permissões por workspace** (`memory_grants`) valem no proxy `/memory-mcp`: o escopo continua vindo do token e outro workspace da empresa só é alcançável com concessão. Com `none` a funcionalidade fica desligada com mensagem clara. A fronteira do item 5 se mantém: o ai-memory não é fonte de verdade de estado do Hanko; propostas, decisões e concessões moram no Postgres.
