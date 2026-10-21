# ADR-0054 — Workspace de execução com Git, padrões de projeto e "Nova sessão"
Data: 2026-10-17 · Status: aceita · Revisa: ADR-0025

## Contexto
O ADR-0025 definiu o workspace como metadado: caminho, `repo_url` e branch padrão, sem que o Hanko clonasse nada ("Nenhum
`git clone` é feito pelo Hanko"). Na prática duas personas na mesma pasta se atropelam, o humano não vê o que a persona
mudou antes de ela terminar, e a credencial de um repositório privado não tinha onde morar. Este ADR cobre os itens #33,
#34 e #16 do marco M10 (`docs/PLANO-DECISOES.md`) e revisa a decisão 1 do ADR-0025: o Hanko passa a clonar e a criar
worktrees, **só quando o operador liga** (`HANKO_GIT_WORKSPACES`) e o admin liga o Git de um workspace.

## Decisão

### A. Workspace com Git (#33)
1. **Interruptor do operador.** `HANKO_GIT_WORKSPACES=true` (padrão desligado, como as fontes de skills do ADR-0035) e
   `HANKO_WORKSPACES_ROOT` configurada. Desligado, nenhuma operação clona e o comportamento é exatamente o anterior.
   Hosts internos só com `HANKO_INTEGRATIONS_ALLOW_PRIVATE`.
2. **Modelo** (migration 00060, só tabelas novas — nenhuma coluna nova em `workspaces`/`projects`/`tasks`/`runs`, para não
   colidir com marcos paralelos nos modelos gerados): `workspace_git` (uma linha por workspace com Git ligado:
   `credential_ref`, `mirror_state` idle|syncing|ready|failed, `base_branch`, `head_sha`, `last_error`, `last_fetch_at`)
   e `task_worktrees` (uma linha por task: `branch`, `base_branch`, `base_sha`, `head_sha`, `path`, `state`
   provisioning|ready|failed|diverged|removed, `removed_reason`). A "repo do projeto" é o `repo_url` do workspace do
   projeto (o projeto já aponta para um workspace, ADR-0025).
3. **Onde fica no disco**, sempre sob a pasta da empresa (`<raiz>/<slug-da-empresa>`): espelho em
   `.hanko/mirrors/<workspace>.git` e worktree em `.hanko/worktrees/<workspace>/<branch>`. A pasta `.hanko` é reservada: um
   workspace com `path` dentro dela é recusado. O caminho do worktree é montado só de slug de empresa, slug de workspace e
   uma branch validada (`hnk-123`), nunca de texto livre.
4. **Espelho.** Repositório bare com os branches do remoto em `refs/remotes/origin/*` (e não em `refs/heads/*`): a busca com
   `--prune` não apaga nem recusa as branches de trabalho das tasks, que moram no mesmo repositório. Sem tags. A URL gravada
   na configuração do espelho não leva credencial. O branch-base é o `default_branch` do workspace ou o HEAD do remoto.
5. **Credencial = segredo.** `credential_ref` é uma única referência `${secret:NOME}` a um segredo **da empresa** (ADR-0050;
   referência `user/` é recusada). Validada na gravação (precisa existir), resolvida por `Service.ResolveSecretText` só na
   hora da busca. Token https vai como cabeçalho `Authorization: Basic` por `GIT_CONFIG_COUNT/KEY/VALUE` (escopado ao host,
   fora do argv e do disco); chave privada ssh vai num arquivo 0600 temporário apontado por `GIT_SSH_COMMAND`, apagado no fim.
   O valor e suas formas derivadas (base64) são removidos das mensagens de erro; atividade, log e API só levam a referência.
6. **git do sistema, sem shell** (`internal/gitrepo`, sobre `procrun`): argv fixo, ambiente mínimo (sem configuração de
   sistema/usuário, sem prompt, sem proxy herdado, `core.hooksPath=/dev/null`, `protocol.allow=never` com o protocolo da
   chamada liberado). O endereço aceito é `https://` (netguard), `ssh://` ou `git@host:dono/repo`; host interno é recusado
   antes de conectar (janela de rebinding de DNS entre a checagem e a conexão, igual ao ADR-0035). `file://` só existe nos
   testes (`GitWorkspacesConfig.AllowFile`; a configuração do servidor nunca o expõe).
7. **Worktree por task.** Ao abrir uma run de task cujo workspace tem Git, o executor chama `PrepareTaskWorktree`: busca o
   espelho se tiver mais de 2 minutos (falha de busca com espelho bom antes = segue e avisa), reivindica a linha
   (`ClaimTaskWorktree`, idempotente e com retomada de provisionamento abandonado após 10 min), roda
   `git worktree add -b hnk-123` a partir de `origin/<base>` e grava `ready`. Se a branch já existe (task retomada), é
   reaproveitada. O diretório da run passa a ser o worktree. Falha ao provisionar **falha a run cedo**, com o motivo, em vez de
   rodar no diretório errado. Runs de revisão só leem o worktree que já existe; runs de sistema e chat não usam. O commit sai
   com o nome da persona (`GIT_AUTHOR_*`). **O Hanko não faz push**: o trabalho vive na branch do espelho.
8. **Estados e reconciliação.** `diverged` = o disco não bate com o registro (pasta sumiu → recria reaproveitando a branch;
   branch trocada/HEAD solto → a run segue com aviso e o estado fica visível até voltar ao normal). `failed` volta a
   provisionar na próxima run. Transições passam por `WorktreeState.CanTransition`.
9. **Limpeza.** Tick `worktree_tick` (a cada 5 min, job river) tira a pasta de worktrees de task `done`/`cancelled`, e de
   worktree cuja branch já está contida na base do remoto (com commits próprios e pasta limpa; a branch integrada é apagada
   do espelho). Antes de apagar, trabalho solto vira um commit de segurança na branch ("nada se perde"); a branch **fica** no
   espelho (apagada só quando integrada), então diff, log e arquivos continuam navegáveis depois. Excluir o workspace limpa
   espelho e worktrees do disco. Desligar o Git com worktree em uso é 409. Cada passo grava `activity_event` na mesma
   transação do estado (`task.worktree_provisioning|ready|failed|diverged|removed`, `workspace.git_*`).
10. **Aba Git** (workspace e projeto; também na task): status (alterações não commitadas), diff contra a base (teto de 512 KiB,
    truncado com aviso), log desde a base, à frente/atrás da base, "integrada", e um **browser de arquivos somente leitura**
    (pasta viva via sistema de arquivos com `EvalSymlinks` confinado ao worktree e `.git` oculto; pasta limpa via `ls-tree`/
    `cat-file`; binário não devolve conteúdo; arquivo até 256 KiB). Nenhuma rota escreve no worktree.
11. **Papéis.** Admin liga/desliga o Git do workspace e limpa um worktree à mão; membro sincroniza o espelho; todos leem.
12. **Fora do escopo** (registrado): serviços/dev server por workspace (decisão #33 do plano); push/PR; ambientes remotos —
    num ambiente SSH/sandbox o diretório é sincronizado sem o espelho e o `.git` do worktree aponta para um caminho absoluto
    daqui, então git dentro do ambiente remoto não funciona.

### B. Padrões de projeto (#34)
1. **Tabela `project_defaults`** (lateral): `instructions` (briefing, até 16 KiB), `goal_id` padrão, `import_repo_skills` e
   `skills_path`. O briefing entra no **system prompt depois das instruções da persona** ("## Instruções do projeto X").
2. **Meta padrão.** Uma task nova do projeto sem meta própria herda `goal_id` quando a meta ainda aceita tasks (uma meta
   encerrada nunca impede criar task; a escolhida de propósito vence).
3. **Política de revisão: já coberta pelo ADR-0040** (`project_review_policies`, `SetProjectReviewPolicy`, API e CLI já
   existem); este ADR só a expõe no formulário de padrões do projeto. **Política de plano** (#19, `kind=plan`) depende do
   gate de plano, que não existe na main: fica para quando o gate entrar (o formulário não ganha campo órfão).
4. **Skills do repositório clonado.** Reaproveita as fontes de skills (ADR-0035): a fonte do espelho tem URL
   `workspace://<id-do-workspace>[/caminho]` (impossível de criar pela API/UI de fontes, que só aceitam https), é lida do
   espelho com `skillgit.ScanLocal` (sem rede, sem clonar de novo) e usa a mesma `syncSource` (entradas, revisões, conflitos
   de slug, `source_modified`). `auto_update=false`: a sincronização acontece a cada busca do espelho de um workspace cujo
   projeto liga `import_repo_skills`, e ao ligar a opção. Independe de `HANKO_SKILL_SOURCES` (nenhum clone novo sai do servidor).

### C. "Nova sessão" na task (#16)
Tabela `task_session_resets`; `RequestTaskNewSession` (membro humano; persona não pede) registra `task.session_reset` na
atividade. O **próximo run** da task consome o pedido na transação do despacho (`run.session_reset_applied`), recebe
`RunContext.SessionReset` e o prompt diz que não há conversa anterior (só briefing e memória). **Limite honesto:** hoje as
runs de task já começam uma sessão nova do harness (só o chat retoma `session_ref`), então o efeito observável é o registro e o
aviso no prompt; o marcador é o contrato que a continuação de runs (#12) deve consultar (`Service.LastSessionReset` +
`domain.SessionUsable`) antes de retomar uma sessão.

## Alternativas descartadas
- Colunas novas em `workspaces`/`projects`: colidiriam nos modelos gerados com marcos paralelos (mesma razão do ADR-0040).
- Clone completo por task: caro e sem objeto compartilhado; o espelho + worktree compartilha o banco de objetos.
- Apagar a branch ao concluir: perderia o trabalho (não há push); a branch fica até ser integrada.
- Trabalhar direto no `path` do workspace: duas personas se atropelam e o humano não vê o diff isolado por task.
- Biblioteca git em Go: dependência nova e enorme; o `git` do sistema é o padrão do mercado e já é pré-requisito de skills.

## Consequências
- Migration 00060 (faixa 00060–00069): `workspace_git`, `task_worktrees`, `project_defaults`, `task_session_resets`. Aditiva; o
  Down desfaz.
- Dependência Go nova: nenhuma. Requer o binário `git` (2.31+, para `GIT_CONFIG_COUNT`) no PATH do servidor quando ligado.
- Env nova: `HANKO_GIT_WORKSPACES`. Pasta reservada `.hanko` na pasta da empresa.
- O espelho e os worktrees ocupam disco; a limpeza é por tick e por exclusão de workspace.
- Export/import (`hanko.company.v1`): não leva configuração Git, worktrees, padrões de projeto nem pedidos de nova sessão
  (são estado desta máquina/instalação).
- Limitações conhecidas: sem push; sem tratamento de submódulos (`submodule.recurse=false`); LFS desligado
  (`GIT_LFS_SKIP_SMUDGE`); janela de rebinding de DNS; ambientes remotos sem git funcional no worktree; busca de espelho e
  criação de worktree serializadas por espelho só dentro do processo (o git faz o resto).
- Testes: repositórios locais `file://` em diretórios temporários (gitrepo, service, heartbeat com adapter fake que commita), sem rede.
