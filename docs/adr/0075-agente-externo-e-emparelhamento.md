# ADR-0075 — Agente externo e emparelhamento de repositório com workspace
Data: 2026-10-25 · Status: aceita (Bardi, 2026-10-25) · Issue: #66 (v0.8)

## Contexto
O v0.8 desliga o agent-studio do oute-agent e traz para o Hanko toda a telemetria dos agentes do oute (#66). Esses
agentes rodam fora do Hanko: o Hanko não os executa, só recebe o que eles emitem. Hoje o Hanko só conhece a persona,
que ele mesmo executa por um harness (ADR-0023). A telemetria do oute traz, em cada fato, a instalação
(`oute_instance`), o host (`host_name`), o agente (`oute_agent`) e o repositório (`oute_repo`), em colunas livres, sem
cadastro (`docker/agent-studio/agent_studio/store.py:26-65@845764f` do oute-agent). O histórico só tem o nome do
repositório, nunca a URL (decisão 14 da #66). Pela decisão 1 da #66, o oute é a empresa "Oute", que já existe no Hanko,
e cada repositório fica emparelhado com um workspace dessa empresa. Este ADR fixa os dois conceitos que os ADRs
seguintes do v0.8 usam. As escolhas abaixo são do Bardi, em 2026-10-25, uma pergunta por vez.

## Decisão
1. **Agente externo.** É cada agente que aparece na telemetria (claude, codex…), no mesmo nível de uma persona, mas
   observado: o Hanko não o executa. Pertence a uma empresa, a da chave de ingestão. A instalação não é um conceito: o
   oute já é a empresa, e `oute_instance` fica só como atributo do fato. O host é um conceito próprio, em ADR seguinte.
   Não se confunde com "canal externo" (ADR-0046), que é um meio de conversa (Telegram, GitHub), não fonte de telemetria.
2. **O agente externo nasce na ingestão.** O primeiro fato com um nome de agente novo cria o agente externo na empresa,
   com `activity_event`, e abre um item na Caixa ("agente externo novo"). Nenhum fato é recusado por agente
   desconhecido. Owner e admin renomeiam, arquivam ou juntam dois agentes externos; cada ação gera `activity_event`.
   Juntar leva o passado do agente juntado.
3. **Emparelhamento.** É o vínculo entre a chave de um repositório e um workspace da empresa. A chave é a URL do remoto
   quando o fato a traz, com o nome do repositório como reserva (decisão 14 da #66). O fato guarda o repositório bruto
   que chegou e, quando houver, a marca de **atribuição inferida**. O workspace do fato sai do emparelhamento vigente.
4. **Como o workspace é achado**, nesta ordem:
   1. Com URL: o workspace com o mesmo `repo_url` normalizado. `https://`, `ssh://` e `git@host:dono/repo` do mesmo
      repositório são iguais; o `.git` final não conta; o host vale em minúsculas.
   2. Sem URL: o workspace cujo `slug` é igual ao nome do repositório.
   3. Nenhum achado: o Hanko cria o workspace, só metadado, sem `path` (ADR-0025 item 1), com `activity_event`.
   4. Conflito (o slug existe, mas com outro `repo_url`): o Hanko não associa por adivinhação. Cria um workspace com
      sufixo (`<slug>-2`) e abre um item na Caixa ("emparelhamento a conferir").
5. **Corrigir o emparelhamento leva o passado junto.** Owner e admin trocam o workspace de um emparelhamento; custo,
   conversas e telemetria antigas passam a contar no workspace novo, com `activity_event` da troca. É a mesma escolha do
   ADR-0025 (o histórico segue a task). O repositório bruto e a marca de atribuição inferida do fato não mudam.
6. **Workspace de sistema `no-repo`.** Cada empresa que recebe telemetria tem um workspace de sistema, slug `no-repo`,
   que recebe todo fato sem repositório. Não se exclui nem se renomeia; o rótulo vem traduzido pela UI ("Sem
   repositório" / "No repository"); não tem `path`. Um fato sai dele quando um emparelhamento posterior o atribui a um
   repositório.
7. **Atribuição inferida na migração.** Os acertos que o agent-studio fez no dado entram como atribuição inferida, sem
   recálculo: o repositório inferido pelo `file_path` (`repo_infer.py:112@845764f`), o #617 (antes de
   2026-10-06T00:00:00Z sem repositório → `oute-agent`) e o #803 (`regression-*` → `oute-agent`)
   (`repo.py:37-87@845764f`). As 2 linhas do repositório `proj` não entram no Hanko e ficam no bucket e na cópia final
   (`docs/research/2026-10-inventario-agent-studio.md` §9.1 ponto 8). A marca de origem "regressão" é da classificação
   de conversa, em ADR seguinte.

## Consequências
- Um nome de agente digitado errado vira um agente externo até owner ou admin juntá-lo ao certo; o item na Caixa avisa.
- Um relatório tirado antes de uma correção de emparelhamento não bate com um tirado depois; o `activity_event` da
  troca explica a diferença.
- Duas instalações do oute na mesma empresa se separam só pelo atributo `oute_instance`, sem tela própria.
- A migração do histórico (só nomes) usa o passo 2 do item 4. Os repositórios cujo nome não é slug de nenhum
  workspace da Oute viram workspaces novos; a lista de nomes e a existência de repetidos ficam para o ensaio da
  migração (decisão 3 da #66).
- Agentes externos, emparelhamentos e o workspace `no-repo` entram no export da empresa (ADR-0022, decisão 6 da #66);
  o formato fica para o contrato do v0.8.

## Alternativas descartadas
- **"Fonte externa"** como conceito acima do agente (a instalação inteira): um nível a mais que a empresa já cobre.
- **"Instalação observada"**: estranho quando a origem não é uma instalação.
- **Cadastro do agente antes da ingestão, com quarentena**: a ingestão passaria a depender de uma pessoa, e o fato
  esperaria fora do lugar.
- **Workspace gravado no fato na ingestão**: um emparelhamento errado ficaria para sempre no histórico.
- **Emparelhar só pela URL**: o histórico inteiro, que só tem nomes, cairia em workspaces novos.
- **Nunca emparelhar sozinho**: contraria a decisão 1 da #66 ("se não existe, o Hanko cria").
- **`no-repo` como workspace comum**: renomeado ou excluído, os fatos sem repositório ficariam sem destino.
