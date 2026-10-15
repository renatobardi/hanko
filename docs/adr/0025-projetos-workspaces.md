# ADR-0025 — Projetos e workspaces de primeira classe
Data: 2026-10-15 · Status: aceita

## Contexto
As tabelas `workspaces` e `projects` existem desde `00001_init.sql`, com `tasks.project_id` e `personas.workspace_id`, mas
só serviam de pano de fundo: sem validação forte do caminho, sem tela, sem orçamento por projeto e com a pasta de
trabalho da run decidida só pela persona. O menu mostrava "Projetos" e "Workspaces" como "Em breve". Era preciso agrupar
tasks, limitar o gasto de uma iniciativa e dizer, de forma auditável, em que diretório cada run rodou.

## Decisão
1. **Workspace.** Nome, slug, `path` (opcional), `repo_url` (opcional) e `default_branch` (opcional). O `path` precisa
   ser absoluto, já limpo (`filepath.Clean(p) == p`: sem `..`, `//` ou `/` final), sem caracteres de controle e
   **estritamente dentro de `HANKO_WORKSPACES_ROOT`**, comparado com `filepath.Rel` depois de resolver os symlinks do
   trecho que já existe no disco (`/raiz-x` não está dentro de `/raiz`, e um link para fora não passa). O `repo_url` é
   só metadado: aceita `https://`, `ssh://` e `git@host:dono/repo`, recusa credenciais embutidas (`https://u:s@…`),
   espaços e outros esquemas (`file:`, `http:`). Nenhum `git clone` é feito pelo Hanko. A branch usa um subconjunto
   seguro das regras do git (começa com letra ou dígito; só `A-Za-z0-9._/-`; sem `..`, `//`, nem `/`, `.` ou `.lock`
   no fim). Um workspace sem `path` é só metadado.
2. **Uso e exclusão.** Detalhe e lista mostram as personas e os projetos que usam o workspace. Excluir um workspace em
   uso devolve 409 (`WorkspaceInUseError`: personas, projetos e tasks abertas que o escolheram de propósito). Com `unlink`
   explícito (API `?unlink=true`, CLI `--unlink`, caixa na UI) o vínculo é desfeito na mesma transação, com activity
   por persona/projeto, e o workspace é excluído.
3. **Projeto.** Nome, slug, descrição, `status` (`active`/`archived`), workspace opcional e `budget_monthly_cents`
   opcional. Progresso = concluídas / (total − canceladas). Projeto arquivado não recebe task nova, mas as tasks
   antigas continuam editáveis e podem sair dele (`clear_project`). Reativar é um `PATCH` de status.
4. **Orçamento do projeto (estende o ADR-0020).** `checkBudgets` ganha o alvo "projeto": soma o gasto real do mês das
   runs de tasks do projeto (atribuído por `tasks.project_id`, então mover uma task move o histórico), mais a reserva das
   runs abertas e a da nova. Estourou: a run é **recusada** com `BudgetBlockedError{Scope:"project"}` (que continua
   `ErrBudgetExceeded`/`ErrConflict`) e a mensagem diz qual projeto, o teto e o gasto. **A persona não é pausada**:
   o teto é do projeto, e a persona segue livre para tasks de outros projetos; a rede de segurança do heartbeat tenta
   de novo quando o teto sobe ou o mês vira. Em ≥ 80% grava `budget_alerts` e `activity_events`
   (`budget.warning`/`budget.exceeded`, sujeito "projeto"), que aparecem na Caixa de entrada e em `/metrics`.
   O custo **pago** (mensalidade rateada de assinatura) não é alocado por projeto: o rateio é por persona e período, e
   dividi-lo por projeto inventaria precisão. O filtro por projeto na tela de custos mostra só custo real/lista/estimado
   e avisa disso.
5. **Pasta de trabalho da run.** Precedência: **workspace da task** (escolha explícita) > **workspace do projeto da
   task** > **workspace da persona** > nenhum (comportamento anterior: diretório temporário). A origem fica em
   `runs.workspace_id` e `runs.workspace_source` (`task`|`project`|`persona`|`none`), gravados no checkout, e aparecem
   na run. A task **deixa de copiar** o workspace do projeto na criação (antes copiava, o que fixava o valor e
   escondia mudanças do projeto); a migration 00016 anula as cópias antigas que coincidem com o workspace do projeto.
   Se o diretório não existe na máquina, a UI mostra "diretório ausente" (aviso, não erro) e o executor usa o
   temporário.
6. **Papéis (ADR-0021).** `viewer` lê projetos, workspaces, custos e progresso; `member` cria e edita tasks (inclusive
   o campo projeto); `admin` e `owner` criam, editam, arquivam e excluem projetos e workspaces e mexem no orçamento do
   projeto. A autorização está no service (`s.require`), nunca só na camada de entrada.
7. **Exportar/importar (ADR-0022).** O formato continua `hanko.company.v1`: workspaces ganham `default_branch` e
   projetos ganham `status` e `budget_monthly_cents`, todos **opcionais**; arquivos antigos importam com os padrões
   (ativo, sem orçamento, sem branch). **Caminho é específico da máquina**: na importação, um `path` que esta instalação
   não aceita (fora da raiz, relativo, com travessia) entra **vazio** (só metadado) em vez de abortar a importação, e a
   contagem `workspace_paths_cleared` informa quantos foram limpos. Repositório, branch e vínculos projeto→workspace
   sobrevivem. Criar/editar workspace continua estrito. Validações de `repo_url`, branch, status e orçamento valem
   também na importação (arquivo editado à mão), e a recusa não deixa empresa para trás.
8. **MCP.** `hanko_project_list` (projetos ativos, ou todos com `all`), e `hanko_task_create`/`hanko_task_list`
   aceitam `project` (slug). Não há tools de escrita de projeto/workspace para agentes.
9. **Interface.** `/projetos` (abas ativos/arquivados/todos, progresso, gasto do mês), página do projeto (tasks,
   progresso, custo do mês, personas, orçamento, arquivar), `/workspaces` (lista, detalhe com quem usa, exclusão com
   desvincular), campo e filtro `projeto` nas tasks, filtro e quadro de orçamento por projeto em Custos. Itens saem de
   "Em breve" em `nav.go`. CLI: `hanko workspace …`, `hanko project …`, `--project` em `task create/list`,
   `hanko task project`, `hanko cost --project`, `hanko budget set --project`.

## Consequências
- O orçamento por projeto limita uma iniciativa sem parar o time; o preço é que a persona continua "acordando" para
  uma task bloqueada até o teto subir (a run é recusada com motivo claro, sem custo).
- Mover task entre projetos reatribui o gasto do mês (decisão consciente: o histórico segue a task).
- Sem alocação do custo pago por projeto, a soma por projeto pode ser menor que o total da empresa; a tela avisa.
- Mudar o workspace de um projeto muda a pasta das próximas runs de suas tasks sem workspace próprio; runs antigas
  guardam a origem no momento do checkout.
- Importar entre máquinas é seguro por padrão (nunca confia em caminho alheio), ao custo de reconfigurar o `path`.
- gosec: a validação do caminho usa só `filepath.Clean`/`filepath.Rel` sobre valor já validado; o binário gosec não
  estava disponível no ambiente de desenvolvimento, então a checagem fica a cargo do CI (golangci-lint).
