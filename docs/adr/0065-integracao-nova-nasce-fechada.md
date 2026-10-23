# ADR-0065 — Integração MCP nova nasce fechada para escrita
Data: 2026-10-23 · Status: aceita · Altera: ADR-0036 (decisão 3, linha de base do catálogo)

## Contexto
Ligar o GitHub (servidor MCP remoto com token) a uma persona em produção expôs três padrões que abrem demais por omissão:
1. **"Vale para todas as personas" vinha marcado** ao cadastrar um servidor MCP. Toda persona recebia o servidor e, sem
   perfil de ferramentas, todas as tools dele, inclusive merge e apagar arquivo, sem perguntar.
2. **A primeira descoberta do catálogo é a linha de base** (ADR-0036, decisão 3): tudo entra `active`, inclusive o que é
   destrutivo. O motivo era não mudar nada para quem já usava a integração antes do catálogo existir; para uma integração
   cadastrada hoje, isso só significa que as tools de escrita nascem liberadas.
3. **A regra por risco do perfil vinha com "Desligado" pré-selecionado**, e o servidor gravava como "Permitido" qualquer
   escolha que não reconhecesse. Um clique sem olhar gravava o oposto do pretendido.

## Decisão
1. **Quem recebe é escolha explícita.** O formulário de servidor MCP novo vem com "Vale para todas as personas"
   desmarcado e avisa que persona sem perfil recebe todas as tools. A API e a CLI não mudam (o campo já era opcional e
   falso por padrão).
2. **Integração nova: a primeira descoberta põe em quarentena o que escreve.** Toda integração MCP criada a partir daqui
   (formulário/API, conexão OAuth e importação) grava na mesma transação uma linha de catálogo **pendente**
   (`CreatePendingToolCatalog`: sem `synced_at`, sem erro). Na descoberta:
   - **sem linha de catálogo** = integração anterior a este ADR → linha de base do ADR-0036, tudo `active` (nada muda para
     quem já a usa);
   - **com linha pendente** = integração nova → só o que declara que só lê nasce `active`; `write`, `destructive` e
     `unknown` entram `quarantined` e aparecem na Caixa para o admin revisar (liberar uma a uma ou todas de uma vez).

   Vale qualquer que seja o gatilho da descoberta: o botão do admin ou o gateway na primeira execução que usa a integração.
3. **Regra por risco sem escolha implícita.** O seletor começa vazio e obrigatório; o servidor recusa escolha vazia ou
   desconhecida em vez de cair em "Permitido".

## Alternativas descartadas
- Quarentena na primeira descoberta só quando um humano a dispara: a descoberta pelo gateway (primeira execução) seguiria
  abrindo tudo, justamente o caso sem ninguém olhando.
- Coluna nova em `integration_tool_catalogs` para marcar "integração nova": a presença da linha já distingue os dois
  casos sem migration.
- Mudar a linha de base também para integrações antigas: quebraria personas em uso no próximo `tools/list`.

## Consequências
- Cadastrar um servidor MCP passa a ter um passo a mais (revisar as tools de escrita), visível no catálogo e na Caixa.
- Caso de borda: uma integração antiga cuja primeira descoberta **falhou** já tem linha de catálogo (o erro é gravado) e,
  ao descobrir com sucesso, segue a regra nova. Ela nunca listou tools, então nenhuma persona perde o que usava.
- Sem migration (só a query nova), sem dependência nova, sem mudança na API.
