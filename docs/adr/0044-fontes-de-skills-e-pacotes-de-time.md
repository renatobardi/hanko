# ADR-0044 — Fork e retrato de skills por run, e pacotes de time (hanko.team.v1)
Data: 2026-10-17 · Status: aceita

## Contexto
O plano pede, no M17: **#36** skills (revisões append-only com pin, fontes git com sync, "fonte read-only + fork", estúdio de teste
isolado) e **#24** pacotes de time `hanko.team.v1` (subset do company.v1, prévia, credenciais exigidas, instala pausado, catálogo
embutido + arquivo/URL, CLI `teams`).

Quase todo o #36 **já existe** desde o ADR-0035 (migration 00031): `skill_versions` imutáveis com sha256 e diff, restauração como versão
nova, `persona_skills.pinned_revision`, `skill_sources` com `discover`/`create`/`sync`/backoff/`skill_tick`, `source_modified`, e o estúdio
(`skill_test_inputs`/`skill_test_runs`, run isolada com persona descartável e custo marcado como teste). Este ADR **não repete** nada
disso; registra o que o ADR-0035 cobre e acrescenta só a diferença. O #24 se apoia no motor de import com prévia e merge do
ADR-0057 (M16b), que o próprio ADR deixou preparado para isso (item 11).

### Já coberto por ADR-0035 (nada a fazer)
- Revisões append-only das skills com sha256, diff e restauração; pin por persona (`pinned_revision`) que vale para tudo o que a run recebe.
- Fontes (repo + path + ref), descoberta antes de gravar, sincronização que gera revisão, desligar/manter cópias.
- Estúdio: teste isolado com persona descartável, só a skill testada, sem MCP da empresa nem memória, custo marcado como teste.
- Política por empresa para skills: **fora** do plano.

## Decisão

### A. Fork de skill (#36, "fonte read-only + fork")
1. **`ForkSkill`** cria uma skill **local** nova a partir de uma skill (a de fonte, tipicamente) na revisão atual ou numa pedida. A cópia
   não tem `source_id` (a sincronização da fonte não a toca), nasce com a **primeira versão própria** e guarda `forked_from_id` e
   `forked_from_revision` (migration 00131). Slug: o pedido ou `<origem>-fork`, `-fork-2`… Admin; atividade `skill.forked`.
2. **Mover personas.** `move_personas` liga a cópia nas mesmas personas e **desliga** a original nelas, na mesma transação (a versão fixada
   não vale na cópia, que tem história própria).
3. **Edição direta de skill de fonte continua permitida**, com a marca "modificada" e a regra de `force` do ADR-0035 (#11): tornar a
   fonte estritamente somente-leitura quebraria quem já usa assim e não acrescentaria segurança, porque o sync já não sobrescreve a
   edição. O fork é o caminho **recomendado** e a UI o oferece na página da skill.
4. **API/CLI/UI.** `POST /companies/{c}/skills/{s}/fork` (`slug`, `name`, `revision`, `move_personas`), `GET .../skills/{s}/forks`;
   `hanko skill fork <skill> [--slug --name --revision --move-personas]`; seção "Bifurcar" e "Cópias locais" na página da skill.

### B. Retrato das skills por run ("pin no run")
5. O pin por persona já decidia **o que** a run recebe; faltava **registrar** o que ela recebeu. `run_skills` (migration 00131: run,
   skill, slug, revisão efetiva, sha256 do corpo, `pinned`) é gravada pelo executor no despacho (`RecordRunSkills`, melhor esforço: falha
   vira aviso no log da run). Imutável por desenho (só some com a run); a skill pode mudar depois e o retrato não.
6. `GET /companies/{c}/runs/{r}/skills`, `hanko run skills <run>` e a linha "Skills" na página da run mostram `slug vN (fixada)`. A run de
   teste do estúdio também grava (a skill testada, na revisão testada).

### C. Pacotes de time `hanko.team.v1` (#24)
7. **Formato.** Um JSON versionado (`internal/export/team.go`) com `package` (slug, nome, descrição, versão, autor), `requires`
   (harnesses e segredos, **calculado** pelo instalador, nunca confiado do arquivo), `personas`, `skills`, `flows` (rotinas) e `projects`.
   Referências por slug, como no company.v1. **Nunca** carrega segredo, assinatura, workspace (caminho de máquina), task nem histórico:
   o validador recusa `workspace` e `subscription` nas personas. Campos desconhecidos são recusados. Tetos: 50 personas, 100 skills,
   50 rotinas, 20 projetos, 2 MiB. `metas` (goals) ficam de fora: não fazem parte do company.v1 ainda.
8. **Instalação = import com merge.** O pacote vira um `company.v1` em memória (`TeamPackage.Document`) e passa pelo `ImportCompany`
   `Into` (ADR-0057): a mesma validação, a mesma prévia (itens com ação por item, avisos), o mesmo conflito por item (`skip`
   padrão, `rename`, `replace`) e tudo-ou-nada numa transação. Personas entram **pausadas** e rotinas **desligadas** (o instalador nunca
   passa `Active`). Admin da empresa, só humano; empresa arquivada recusa.
9. **Passo de assinaturas/harness.** A prévia lista cada harness exigido e as assinaturas **ativas** da empresa para ele; quem instala
   escolhe, por harness, uma assinatura **existente** (`subscriptions: {claude: "Plano"}`) e as personas daquele harness passam a usá-la.
   A assinatura nunca é criada nem alterada pelo pacote (a ação de conflito dela é sempre `skip`). Sem escolha e sem assinatura ativa,
   um aviso diz quantas personas rodam com o login do harness.
10. **Credenciais exigidas.** Os `${secret:NOME}` citados na `adapter_config` das personas viram a lista de segredos do pacote; a prévia
    diz quais **existem** e quais **faltam**, com aviso. O pacote cita nomes, jamais valores.
11. **Catálogo embutido + arquivo/URL.** `internal/teams` embute pacotes em `catalog/*.json` (squad de produto, mesa de conteúdo, suporte
    nível 1); nada é baixado pelo servidor. Arquivo e **URL são lidos pela CLI** (`hanko teams preview|install <arquivo|URL|catalog:slug>`)
    e enviados no corpo; o servidor nunca busca uma URL de pacote (sem SSRF novo). A UI aceita o upload do arquivo (cache em memória por
    usuário, 15 min) e o catálogo.
12. **Exportar.** `ExportTeam` (admin) parte do `ExportCompany`, escolhe as personas (todas, se nenhuma), leva as skills que elas usam e as
    rotinas delas (desligadas), e **corta** `reports_to`/`reviewer` que apontam para fora do subset. Os textos já saem redigidos
    (`adapter.Redact`) e a configuração sem chaves sensíveis (`ScrubConfig`).
13. **Registro.** `team_installs` (migration 00132: pacote, versão, origem `catalog|file`, sha256, contagens, quem) e a atividade
    `team.installed` (mesma transação) — a instalação em si gera as atividades do import (`company.import_merged` e as de cada item).
    O catálogo legado da tela de Personas (ADR-0031, times fixos que contratam personas ativas) continua; o novo fica na mesma página.
14. **Superfícies.** API: `GET /team-catalog[/{slug}]`, `GET /companies/{c}/team-export`, `POST /companies/{c}/teams/preview|install`,
    `GET /companies/{c}/team-installs`. CLI `hanko teams catalog|show|export|preview|install|history`. UI: seção "Pacotes de time" no
    Catálogo de times (prévia com troca de ação por item e seletor de assinatura, upload, exportar, histórico). OTel: spans
    `team.export`/`team.install` (mesmo contador `hanko.company.lifecycle`).

## Consequências
- Migrations 00131 (fork, `run_skills`) e 00132 (`team_installs`); sem dependência Go nova.
- O retrato por run cresce uma linha por skill por run (some com a run); índices por skill e por empresa.
- `ExportTeam` lê a empresa inteira como o export comum (inclui tasks no snapshot e descarta); aceitável para o volume atual, e um
  construtor só de configuração fica para depois se pesar.
- Um pacote com `replace` troca instruções de personas existentes: o operador vê isso na prévia antes de confirmar.
- Pacote não carrega integrações nem conexões: elas dependem de segredos e de autorização humana (ADR-0043).
