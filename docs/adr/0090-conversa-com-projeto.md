# ADR-0090 — Conversa com projeto: workspace e ambiente do projeto no chat e na sala
Data: 2026-11-08 · Status: proposta (item 5 do diagnóstico da produção, aprovado pelo Bardi em 2026-11-07; a escolha entre as alternativas é do autor e vale com o merge do PR) · Usa: ADR-0025, ADR-0027, ADR-0033, ADR-0055

## Contexto
Uma run de task escolhe a pasta de trabalho por precedência (ADR-0025 §5): workspace da task, senão o do projeto, senão o da
persona, senão um diretório temporário. A run de chat (ADR-0027) e a de sala (ADR-0033) não têm task nem projeto, então só
olhavam o workspace da persona. Na produção da `oute`, as personas não têm workspace próprio (o repositório é do projeto), e
todo chat sobre o projeto rodava num diretório temporário: a persona não via os arquivos e respondia como se o repositório
não existisse. O mesmo valia para o ambiente de execução (ADR-0055): o do projeto nunca era considerado no chat.

Alternativas vistas:
1. Inferir o projeto pela persona (único projeto em que ela tem tasks). Implícito e instável: a persona muda de projeto, e
   uma conversa antiga passaria a rodar noutra pasta sem ninguém pedir.
2. Dar um workspace à conversa diretamente. Resolve a pasta, mas não o ambiente nem a memória do projeto, e cria um terceiro
   lugar para escolher workspace além de task e projeto.
3. **Dar um projeto à conversa** e reaproveitar a precedência das tasks. Escolhida.

## Decisão
1. **Modelo.** `conversations.project_id` (opcional, FK `ON DELETE SET NULL`; migration 00218) vale para chat direto e sala. É
   escolhido ao abrir a conversa e não muda depois (como a persona do chat). Apagar o projeto solta a conversa, que segue
   valendo sem ele.
2. **Pasta de trabalho.** A run de chat e a de sala usam `pickWorkspace(nil, projeto da conversa, persona)`: **projeto >
   persona > temporário**, a mesma ordem de uma task sem workspace próprio. A origem fica em `runs.workspace_source`
   (`project`, `persona`, `none`), como hoje.
3. **Ambiente de execução.** `recordRunEnvironment` passa a resolver o ambiente de uma run com conversa pelo projeto dela
   (persona > projeto > padrão da empresa > local, ADR-0055), igual à run de task.
4. **Superfícies.** API: `project` (id ou slug) no `POST /conversations` para os dois `kind`, e `project_id` na saída.
   CLI: `hanko chat <persona> "…" --project <slug>`. UI: seletor "Projeto (opcional)" nos formulários de nova conversa e de nova
   sala (só aparece quando a empresa tem projeto), e um chip com o projeto na página da conversa.
5. **Fora deste ADR.** Trocar o projeto de uma conversa aberta; ledger de custos por projeto para runs de chat (seguem na linha
   "Chat", ADR-0027 §2); memória do projeto no prompt do chat (hoje o chat lê a memória da persona; ligar à do projeto é um
   passo seguinte, com ADR próprio se mudar o formato do prompt).

## Consequências
- Um chat aberto com projeto trabalha na mesma pasta (e no mesmo worktree Git, ADR-0054) que as tasks do projeto. Duas runs
  simultâneas na mesma pasta já eram possíveis entre tasks; o chat entra na mesma regra.
- Conversas existentes não mudam: sem projeto, nada muda.
- Testes: `TestConversationProjectWorkspace` (service) e `TestAPIConversationProject` (api).
