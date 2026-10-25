# ADR-0078 — Conversa externa, sessão externa e papel da chamada
Data: 2026-10-25 · Status: proposta (aguarda aceite do Bardi) · Issue: #66 (v0.8) · Usa: ADR-0075, ADR-0077

## Contexto
Acima da chamada ao modelo, o agent-studio agrupa a telemetria em dois níveis (`agent_studio/sessions.py:1-5`,
`conversations.py:1-5`, `usage.py:58-60@845764f` do oute-agent):
- **conversa** = `session.id` do agente (uma conversa do claude ou do codex), com início, fim, host, agente, instância,
  serviço, assinatura, repositório, fase, chamadas, tokens, custo, p95 e erros, tudo pela hora do fato;
- **sessão** = `oute.task.id` (uma sessão do `oute-task`, uma worktree), que agrupa conversas e tem repositório, slug,
  agente, estado, rodada e worker. Conversa sem `oute.task.id` fica em "sem sessão".

O agent-studio também dá um **papel** a cada chamada e mostra as 20 sessões e conversas mais caras, com o contexto
(entrada + cache lido, média e máximo) (`usage.py:57,82-108`; `medicao.py:14-40@845764f`). No Hanko, "conversa"
(`conversations`, ADR-0027) é o chat de um humano com uma persona, e `domain.Session` (`internal/domain/user.go:57`) é
a sessão de login. As escolhas abaixo são do Bardi, em 2026-10-25.

## Decisão
1. **Conversa externa** (`external_conversation`). É a conversa de um agente externo, identificada pelo `session.id`
   que ele emite, dentro da empresa. Não é o chat do ADR-0027 nem uma run: o Hanko não a executa. Atributos de uma
   conversa externa (host, agente, instância, serviço, assinatura, repositório) são o primeiro valor presente pela hora
   do fato, como hoje (`conversations.py:37-38@845764f`). O workspace sai do repositório pelo emparelhamento
   (ADR-0075).
2. **Sessão externa** (`external_session`). É o agrupamento de conversas externas que a fonte declara pelo
   `oute.task.id`. Não é a sessão de login. Conversa externa sem sessão fica fora de qualquer sessão externa e aparece
   no grupo "sem sessão".
3. **Papel é da chamada ao modelo**, não da conversa: uma conversa pode ter chamadas de papéis diferentes. A regra e a
   ordem são as de `usage.py:82-108@845764f`: `subagent` (span com `agent_id` ou `query_source agent.…`), `reviewer`
   (`oute.swarm.step` sem sessão de worker), depois `worker`, `dispatcher` ou `standalone` pelo evento de abertura da
   sessão e, sem ele, pelo resource.
4. **Identificadores em inglês.** Todo valor, slug, tipo, alerta e evento novo do v0.8 é em inglês; só a UI traduz,
   pelo i18n, para pt-BR. Na migração, `subagente` vira `subagent`, um a um; os outros quatro papéis já estão em inglês.
   O mapeamento fica registrado no ensaio, para a conciliação (decisão 3 da #66).
5. **Medição** mantida: as sessões e conversas externas mais caras da janela, com o contexto médio e máximo por chamada
   (`medicao.py:14-40@845764f`), entram na API de uso estendida (bloco 3 do gate da #67).
6. **Nome.** A conversa externa tem nome do gerador único (ADR seguinte). Na migração, cada conversa grava o nome que
   tem hoje, e ele não muda (§9.1 ponto 7 do inventário).

## Consequências
- Na UI, "Conversas" do chat e "Conversas externas" convivem; o i18n escolhe rótulos que não se confundam.
- A sessão externa é declarada pela fonte: um `oute.task.id` repetido entre hosts junta conversas de hosts diferentes,
  como hoje.
- A fase da conversa e a marca de origem "regressão" são da classificação de conversa, em ADR seguinte; a rodada e o
  worker, da rodada externa.
- Conversas e sessões externas entram na busca, na Caixa quando couber e no export (decisão 6 da #66).

## Alternativas descartadas
- **"Run externo"** para a conversa: mistura o que o Hanko executa com o que observa; a conversa dura dias e muitos
  turnos, a run é uma execução do heartbeat.
- **"Tarefa externa"** para a sessão: "task" já é o item do board do Hanko.
- **Papel por conversa**: perderia a separação entre as chamadas de subagente e as da conversa principal.
- **Papéis exatamente como hoje (`subagente` em pt-BR)**: o Bardi quer inglês em todo identificador.
