# ADR-0041 — Gates estendidos: pergunta, plano, reenvio, contratação, persona principal e incidentes de orçamento
Data: 2026-10-17 · Status: aceita

## Contexto
O M15 do plano (itens #18, #19, #21, #8, #9 e #29) aumenta o que um gate sabe fazer. Já existem, desde os ADRs 0021, 0030,
0036, 0052 e 0053, os gates `approval`, `task_review`, `hire` (só decisão, sem efeito), `memory`, `tool` e `instruction`,
a revisão automática antes do gate, a precedência de revisão task > projeto > persona (ADR-0040), os alertas de orçamento
(`budget_alerts`) com pausa automática de persona e o retorno dela quando o teto sobe. Este ADR **não repete** isso: só
acrescenta a diferença. A migration é a **00110**.

### Já coberto (nada a fazer)
- **#8, metade humana:** admin ou mais contrata direto (`HirePersona`, ADR-0021). O gate `hire` já existia como pedido
  livre; o que faltava era a ficha e o efeito.
- **#29, a parte automática:** alerta 80 %/100 %, pausa da persona com `pause_reason`, recusa de run de projeto, reserva
  por run aberta e a retomada ao subir o teto (`resumeBudgetPaused`) já valem (ADR-0019/0020). O incidente é a camada de
  **decisão humana** em cima disso.
- **#21, a revisão automática** de `task_review` já roda de novo a cada tentativa de fechar a task (novo gate); o reenvio
  no mesmo gate vale para os demais tipos (abaixo).

## Decisão

### 1. Gate `question` (#18)
`gates.kind = 'question'`, com o formulário em `gates.payload` (`{"fields":[...]}`): campos `single`, `multi` ou `text`,
até 12 campos e 20 opções, chave `[a-z][a-z0-9_]*`, obrigatório ou não. A persona abre pelo `hanko_gate_request`
(`kind=question`, `questions`); o humano responde pelo formulário na tela do gate, na Caixa, pela API
(`POST /gates/{id}/answer`) ou pela CLI. A resposta é validada contra o formulário, gravada em `gates.answer`
(`{"values":{"chave":["a"]}}`), o gate vira `approved`, a resposta vira um comentário de sistema na thread da task (em
markdown, na ordem do formulário) e a persona é acordada com o gatilho `gate`. **Dispensar** (`dismissed`, com nota
opcional) fecha sem resposta e acorda a persona; **reabrir** devolve a `pending`. Quem responde: `member` ou mais (como
`approval`). A pergunta bloqueia a persona (`awaiting_gate`) como a aprovação.

### 2. Gate `plan` e o plan-review (#19)
Opt-in por **task, projeto ou persona** (tabela `plan_policies`; precedência task > projeto > persona, domínio puro em
`domain.ResolvePlanPolicy`). Sob a política e **sem plano aprovado** na task, o run está na fase `plan`: só planeja.
- O prompt diz isso; o `claude` roda em `--permission-mode plan` sem `Edit/Write/NotebookEdit` (o adapter ganha
  `Spec.PlanOnly`), o ACP e o codex entram em modo somente leitura para as tools nativas, e o `/mcp` recusa, por
  middleware, toda tool `hanko_*` fora de uma lista de leitura + `hanko_gate_request`/`hanko_artifact_put`/
  `hanko_task_comment` (mesmo padrão do teste de skill, ADR-0035). Não há como plan-review "valer" sem isso: a regra mora
  no servidor, não no prompt.
- O plano é um **artefato** (`hanko_artifact_put`, ex. `plano.md`, versionado) ou texto inline; a persona o submete com
  `hanko_gate_request(kind=plan, artifact=<nome|id>)`. O texto do artefato é copiado para o corpo do gate na hora do
  pedido (o que o humano viu fica no gate), e o `payload` guarda o id e a versão do artefato.
- `approved` → o próximo run está na fase `execute`: o briefing traz o plano aprovado (`## Plano aprovado`). `changes_requested`
  → a persona é acordada com a nota e **replaneja** (a task continua sem plano aprovado). `rejected` → a task volta a
  `todo`, como em todo gate.
- A fase é derivada (política + existe gate `plan` aprovado na task), nunca gravada: reabrir a política muda a fase sem
  migração de dados. `GET /tasks/{ref}/plan-policy` mostra a política por camada e a fase.

### 3. Reenvio com revisões e audiência (#21)
- `gates.revision` (1 na criação) e `gate_revisions` (append-only, trigger de imutabilidade como `persona_instruction_revisions`):
  ao **reenviar**, o texto vigente (título, corpo, payload, nota do humano) vira a revisão N em `gate_revisions`, o gate
  recebe o texto novo, `revision = N+1` e volta a `pending` (limpa a decisão), a persona volta a `awaiting_gate`. Só a
  persona que pediu (ou um humano, pelo mesmo caminho) reenvia, e só um gate em `changes_requested` de tipo `approval`,
  `plan` ou `hire`. O diff entre a revisão anterior e a vigente é calculado na leitura (`internal/textdiff`), nunca
  gravado. Cada reenvio é `gate.resubmitted` na atividade, na mesma transação.
- `gates.audience` (`{"users":[uuid],"roles":["admin"]}`): vazia = quem o tipo já exige. Preenchida: decide quem está na
  lista de usuários **ou** tem um dos papéis, **mais o owner da empresa, sempre** (um gate nunca fica órfão). A Caixa e a
  contagem dela escondem de quem não está na audiência os gates pendentes restritos; a lista e o detalhe do gate seguem
  legíveis a todo membro (ADR-0021: ler tudo da empresa). Quem fora da audiência tenta decidir recebe `ErrForbidden`.

### 4. Contratação por gate (#8)
`gates.payload.spec` = `domain.HireSpec` (nome, agente, modelo, instruções, ficha de revisão, orçamento…). A persona pede
com `hanko_hire_request`; o gate nasce `pending` com a ficha validada **sem banco** e depois **com banco** (slug livre,
persona de `reports_to` existente). **Aprovar cria a persona na mesma transação da decisão** (`persona.hired` com
`via_gate`), reportando a quem pediu quando a ficha não diz outro chefe. A ficha **não aceita `env`** no `adapter_config`
(ela fica em texto claro na trilha; segredos entram depois, ADR-0050). Um gate `hire` sem ficha (pedido livre antigo) segue
só como decisão. `company_policies.require_hire_gate`: quando ligada, `HirePersona` direto por humano responde
`ErrConflict` com a instrução de abrir o gate (`POST /personas/hire-request`); o humano admin abre o gate e um admin o
decide (inclusive ele mesmo: a política obriga o passo e a trilha, não duas pessoas — uma empresa de um dono só não
trava). No máximo 5 gates `hire` pendentes por persona.

### 5. Persona principal e de sistema (#9)
- `member_primary_persona (company_id, user_id, persona_id)`: o padrão da pessoa. Uma task criada por esse humano sem
  responsável nasce com ela; a tela de novo chat e `hanko chat` a sugerem. (O plano diz `membership.primary_persona_id`;
  uma tabela própria evita criar linha em `company_members` para o dono da instalação, que é owner de tudo sem linha.)
- `personas.system`: personas de sistema (curadoria de memória, revisor). Não podem ser desligadas (`status=off`) e só um
  **owner** as edita; o owner as marca/desmarca (`PUT /personas/{id}/system`). Nenhuma persona é marcada por backfill:
  o owner escolhe.

### 6. Incidentes de orçamento (#29)
`budget_incidents`: um por (alvo, mês) quando um teto de empresa, projeto ou persona estoura (aberto no mesmo ponto do
`budget.exceeded`, na mesma transação; reabre se o teto estoura de novo no mês depois de resolvido). Aparece na Caixa
(entra em `Count`). Resolver (`POST /budget-incidents/{id}/resolve`, admin) com **uma** destas:
- `ceiling_raised`: sobe (ou remove) o teto do alvo e o que estava pausado **por orçamento** volta
  (`resumeBudgetPaused`, já existente); personas pausadas por um humano não voltam;
- `kept_paused`: registra a decisão e tira da Caixa; nada muda;
- `released_once`: libera **um** run avulso sem tocar no teto (`release_runs`; o próximo run que o `checkBudgets` barraria
  por esse alvo consome o crédito) e retoma só a persona pausada por esse orçamento.
Se o teto sobe por outro caminho (`SetCompanyBudget`, edição de persona/projeto) e deixa de estar estourado, o incidente
se resolve sozinho (`auto`). Cada resolução é `budget.incident_resolved` na atividade com quem decidiu.

## Consequências
`gates_kind_check` ganha `question` e `plan`; `gates_status_check` ganha `dismissed`. Os testes e a UI que enumeram
estados precisam conhecer o novo valor. A fase de plano depende de o harness respeitar o modo somente leitura: no
`claude` é imposto pelo próprio CLI; no `codex`/ACP, pelo sandbox/política de permissão do adapter; em qualquer
harness, a escrita no Hanko (tasks, metas, casos) é recusada pelo servidor. Um harness sem modo somente leitura que
escreva arquivos locais é o limite conhecido (documentado em `docs/validacao/m15.md`).
