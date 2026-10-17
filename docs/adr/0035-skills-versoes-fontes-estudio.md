# ADR-0035 — Skills: versões, fontes git e estúdio de teste
Data: 2026-10-16 · Status: aceita

## Contexto
O ADR-0029 trouxe as skills: um `SKILL.md` por skill, `version` que sobe a cada mudança e o vínculo com as personas.
Três lacunas apareceram no uso:
- **Não há história.** O `version` sobe, mas o conteúdo anterior se perde: não dá para ver o que mudou, voltar atrás nem
  manter uma persona numa versão conhecida enquanto a skill evolui.
- **Skills mantidas fora do Hanko.** Times guardam skills em repositórios git e querem importá-las e mantê-las em dia sem
  copiar e colar.
- **Ligar às cegas.** Para saber se uma skill funciona é preciso ligá-la numa persona de verdade e esperar uma task.

As três mexem com o que chega ao contexto do agente; a das fontes também tira o servidor para a rede e lê conteúdo de
terceiros. Por isso a decisão trata, junto, de imutabilidade, limites, SSRF, execução e custo.

## Decisão

### A. Versões imutáveis
1. **`skill_versions`** (skill, empresa, `revision` = o `version` da skill naquele momento, nome, descrição, corpo,
   `sha256` do corpo, `source_commit` opcional, autor, data; único por skill e revisão). Toda escrita que muda nome,
   descrição ou corpo grava a versão **na mesma transação** — criar, editar, importar arquivo (ADR-0022), sincronizar
   fonte e restaurar. Versões nunca mudam; somem só com a skill. A migration `00031` faz o backfill: o conteúdo atual de
   cada skill vira a versão corrente (as anteriores não existiam em lugar nenhum).
2. **Restaurar** cria uma versão **nova** com o conteúdo antigo (`skill.restored` na atividade); restaurar o que já é o
   atual não muda nada. A tela "Versões" mostra o diff entre versões (`internal/textdiff`).
3. **Versão fixada.** `persona_skills.pinned_revision` (chave estrangeira composta para a versão, então só aceita versão
   existente): a persona usa aquela revisão em vez da mais nova. Vale para tudo o que a run recebe — o `SKILL.md` do
   claude, o índice do codex/acp e o `hanko_skill_get`. Fixar antes de ligar cria o vínculo desligado. Sem fixação, vale
   sempre a mais nova.

### B. Fontes git
4. **O que é.** `skill_sources` (empresa, URL, `tracking_ref` = branch ou `HEAD`, `path_prefix`, `excluded_paths`,
   `auto_update` (padrão ligado), `import_new` (padrão desligado), `enabled`, último commit e árvore, estado
   `idle|syncing|ok|error|paused`, erro, falhas seguidas, próxima sincronização; único por empresa, URL e ref) e
   `skill_source_entries` (cada diretório com `SKILL.md` encontrado: caminho, nome, descrição, skill importada,
   `selection` `selected|excluded|new`, presente, tem scripts, erro). A skill importada guarda `source_id`,
   `source_path` e `source_commit`.
5. **Interruptor do operador.** Clonar sai do servidor: só com `HANKO_SKILL_SOURCES=true` (padrão desligado). Desligado,
   toda operação que clona responde 422 com o motivo, a aba Fontes explica e o tick não faz nada. Uma empresa
   comprometida não liga isso sozinha. Só admin mexe em fontes; qualquer membro as lê.
6. **Destino.** A URL é `https`, porta padrão, sem usuário e senha, sem query nem fragmento, e o host passa pelo
   `internal/netguard`: IP literal interno é recusado e o nome é **resolvido antes do clone**, recusando se qualquer
   endereço for interno (loopback, link-local, RFC 1918, ULA, CGNAT...). `HANKO_INTEGRATIONS_ALLOW_PRIVATE` (a mesma
   decisão de operador das integrações) libera hosts internos para quem tem um servidor git na própria rede.
7. **Clone.** O `git` do sistema por `procrun` (argv, sem shell, grupo de processos morto ao fim) com ambiente mínimo:
   `GIT_TERMINAL_PROMPT=0`, sem configuração do sistema nem do usuário, `HOME` temporário, sem proxy herdado, sem LFS; e
   `-c protocol.allow=never -c protocol.https.allow=always -c http.followRedirects=false -c core.hooksPath=/dev/null
   -c credential.helper=` em **toda** chamada. `clone --depth 1 --single-branch --no-tags --no-checkout
   --filter=blob:limit=64k` num diretório temporário apagado no fim; teto de 60 s para a busca inteira e de 50 MiB em
   disco (vigiado durante o clone; passou, aborta). O commit é resolvido (40 hex) e gravado.
8. **Leitura sem checkout.** Nenhum arquivo do repositório é extraído para o disco: a árvore vem de `git ls-tree -r -z`
   e só os `SKILL.md` (nome sem diferenciar caixa) são lidos do banco de objetos (`git cat-file`). Links simbólicos e
   submódulos são ignorados (um `SKILL.md` que é link vira erro na entrada). **Só markdown:** scripts e executáveis
   (bit de execução ou extensão de script) nunca são copiados nem executados; a entrada ganha a marca "tem scripts — só o
   SKILL.md foi importado". Valem os limites do ADR-0029 (32 KiB de corpo, 120 de nome, 400 de descrição) e no máximo
   200 skills por fonte (o resto é ignorado com aviso).
9. **Fluxo de adicionar.** `discover` clona e lista o que achou, sem gravar (com conflito de slug, scripts e erros) → o
   admin escolhe → `create` grava a fonte e faz a primeira sincronização com a seleção (o que não foi escolhido fica
   `excluded`). A fonte fica criada mesmo se essa sincronização falhar. Depois, `import_new` decide se uma skill nova na
   fonte entra sozinha ou fica `new` para revisão.
10. **Sincronizar.** Reivindica a fonte (`syncing`; uma sincronização presa há mais de 10 min é retomada), clona fora de
    transação e aplica o retrato numa transação só: entradas novas e atualizadas, as que sumiram ficam `present=false`
    (a skill continua), selecionadas sem skill são importadas e as importadas são atualizadas — **só quando o commit ou
    a árvore mudaram** (ou com `force`). Skill com slug já existente na empresa não é tomada: a entrada fica com o erro.
    Sucesso agenda a próxima em 1 h; falha marca `error` com backoff `min(60 min, 5 min·2^min(n-1,4))` (5, 10, 20, 40 e
    60 min) e `skill_source.sync_failed` na atividade. Agendamento: o job periódico `skill_tick` do river (a cada
    5 min) sincroniza as fontes ligadas com `auto_update` e `next_sync_at` vencido, uma de cada vez. "Sincronizar agora"
    é manual.
11. **Editar uma skill de fonte.** Editar (ou restaurar) aqui marca a skill como **modificada localmente**
    (`skills.source_modified`): a sincronização não a sobrescreve sem `force`, a entrada mostra o motivo e a UI exibe o
    selo "modificada". Sincronizar com `force` traz o conteúdo da fonte e limpa a marca. Excluir uma entrada importada
    solta a skill da fonte (ela fica como skill local); apagar a skill tira a entrada da seleção, para a próxima
    sincronização não a recriar.
12. **Desconectar.** O admin escolhe **manter cópias** (as skills ficam, sem fonte) ou **apagar** as skills importadas.

### C. Estúdio
13. **Testar antes de ligar.** `skill_test_inputs` (entradas salvas em markdown, até 32 KiB, no máximo 50 por skill) e
    `skill_test_runs` (skill e revisão, cópia da entrada, persona, task de sistema, run, estado
    `queued|running|done|failed|cancelled`, saída, erro). Rodar um teste cria uma **task de sistema** atribuída à
    persona escolhida (título `Teste de skill — <slug> vN`, descrição = a entrada, sem virar fato na memória) acordada
    com o gatilho `skill_test`, pela máquina normal: checkout atômico, reserva de orçamento, ledger de custo, alertas e
    log ao vivo. `createRun` força o gatilho `skill_test` para a task do teste, seja qual for o caminho que a acordou (a
    rede de segurança do heartbeat, por exemplo); a task não acorda por comentário nem por "Acordar" manual.
14. **A run de teste é isolada.** Recebe **só aquela skill naquela versão** (no lugar das habilitadas da persona), a
    entrada como prompt, as instruções da persona e um aviso de teste; **nenhum MCP da empresa nem a memória** (sem
    briefing, sem handoff, sem fechar sessão), e roda num diretório temporário, nunca no workspace da persona. O MCP do
    Hanko continua (é por ele que codex e acp leem a skill), mas numa run de teste só `hanko_whoami`,
    `hanko_skill_list` e `hanko_skill_get` respondem — e com a skill testada; as demais tools são recusadas, então o
    teste não age na empresa.
15. **Fim.** Ao terminar a run (qualquer desfecho, inclusive cancelada na fila ou interrompida), o resumo vira a
    `output` do teste (até 64 KiB) e a task de sistema fecha. Reexecutar repete com a mesma versão, entrada e persona.
    Cancelar cancela a run, se houver, e fecha a task. Um teste que fica 6 h sem run ativa (persona parada, orçamento
    estourado) falha sozinho pelo `skill_tick`. A UI compara dois testes lado a lado, com o diff das saídas.
16. **Só admin roda teste** (gasta dinheiro), salva entradas e cancela; qualquer membro lê.

### Transversal
17. **Superfícies.** API `/companies/{c}/skills/{s}/versions[/{rev}]` (diff com `?against=N`), `/skills/{s}/restore`,
    `pinned_revision` em `PUT /personas/{p}/skills/{s}` (e em `/skills/{s}/personas/{p}`; `0` solta),
    `/skill-sources` (lista, cria), `/skill-sources/discover`, `/skill-sources/{id}` (GET, PATCH de configurações e
    seleção, DELETE com `keep_skills`), `/skill-sources/{id}/sync` (`force`), `/skills/{s}/test-inputs` (CRUD),
    `/skills/{s}/test-runs` e `/skill-test-runs/{id}` (com `/cancel` e `/rerun`). Run tokens só leem. CLI
    `hanko skill versions|restore|pin`, `hanko skill source list|add|sync|rm` e `hanko skill test <skill> --persona p
    --input arquivo` (espera e imprime a saída). UI dentro de Skills, sem item de menu novo: abas Biblioteca (filtros
    Todas/Locais/De fonte, selos "de fonte" e "modificada"), Fontes (estado, último commit, Sincronizar, Configurar,
    Desconectar; adicionar com descoberta e seleção em árvore por caminho) e Estúdio (skill, persona, versão, entrada
    salva ou texto livre, lista com estado ao vivo por polling, saída em markdown sanitizado, comparar); na página da
    skill, abas Visão geral, Versões (diff, restaurar) e Personas (ligar e fixar versão).
18. **Export/import (ADR-0022).** Nada muda no `hanko.company.v1`: as skills continuam saindo como skills (as importadas
    de fonte saem como skills comuns, com o conteúdo atual). Ficam de fora, de propósito, as versões (história desta
    instalação), as versões fixadas (dependem das versões), as fontes (exigem o interruptor do operador do destino e
    uma nova sincronização) e o estúdio (estado de operação). A importação grava a primeira versão de cada skill.
19. **Slugs reservados.** `fontes` e `estudio` são abas fixas da UI e não valem como slug de skill (somam-se a `nova` e
    `preview`, ADR-0029).

## Consequências
- Migration `00031`: cria `skill_versions` (com backfill), `skill_sources`, `skill_source_entries`,
  `skill_test_inputs`, `skill_test_runs`; acrescenta `persona_skills.pinned_revision` e `skills.source_id`,
  `source_path`, `source_commit`, `source_modified`. O Down desfaz tudo. Os gatilhos e a constraint de `runs` vêm da
  base comum (`00027`).
- A checagem de destino das fontes é feita **antes** do clone, porque é o `git` que conecta: entre a resolução e a
  conexão há uma janela de rebinding de DNS, e um proxy do ambiente não é usado. Quem precisa de mais garantia mantém
  `HANKO_SKILL_SOURCES` desligado ou isola a saída de rede do servidor. Redirecionamentos não são seguidos.
- O filtro de blobs só poupa banda quando o servidor git o entende; o teto de 50 MiB em disco vale de qualquer jeito.
- Um teste gasta orçamento e conta como execução da persona (inclusive para o contador de falhas que a põe em erro).
- O resumo da run de teste também fica como comentário da task de sistema, como em qualquer run.
- Não há credenciais de repositório privado: só fontes públicas. Repositórios privados ficam para depois, com segredos
  pelo `internal/secretbox`.
