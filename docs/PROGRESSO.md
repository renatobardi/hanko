# Progresso do plano de evolução (70 itens)

Status: `pendente` · `em andamento` · `feito` · `parcial` · `bloqueado` (motivo nas notas) · `fora` (decidido não fazer). Fechamento do plano em 2026-10-22. «Commit» é o merge do PR na `main`; as Ondas 2B e 3 ainda estão em PR aberto. «feito» significa entregue com testes; as notas listam o que só foi exercitado contra fakes.

| # | Item | Marco | Status | Commit | Notas |
|---|---|---|---|---|---|
| 1 | Tasks › Dependências bloqueantes | M14 | feito | d4e13e8 |  |
| 2 | Tasks › Árvore pai/filho | M14 | feito | d4e13e8 |  |
| 3 | Tasks › Pausa de árvore | M14 | feito | d4e13e8 | pausa não mata run em andamento (só cancela as da fila); decisão do ADR-0040 |
| 4 | Tasks › Política por task | M14 | feito | d4e13e8 |  |
| 5 | Tasks › Anexos | M8 | feito | c509f5a | drag-and-drop só verificado por teste de integração, sem navegador real |
| 6 | Tasks › Rascunhos | M8 | feito | c509f5a |  |
| 7 | Personas › Instruções | M8 | feito | c509f5a | revisões append-only + gate `instruction` |
| 8 | Personas › Contratação | M15 | feito | bc620ad | a metade humana (admin contrata direto) já existia |
| 9 | Personas › Principal e built-in | M15 | feito | bc620ad | nenhuma persona é marcada como de sistema por backfill: o owner escolhe |
| 10 | Personas › Readiness | M11 | feito | 029e398 | o probe não chama o modelo (modelo inexistente só falha no 1º run) |
| 11 | Chat | M8 | feito | c509f5a |  |
| 12 | Runs › Continuação | M13 | feito | 314eeeb | codex não retoma sessão (só leva o briefing) |
| 13 | Runs › Ações do operador | M13 | feito | 314eeeb | MCP não expõe as ações novas |
| 14 | Runs › Steer | M13 | feito | 314eeeb | nunca testado contra o binário real do claude (só harness falso) |
| 15 | Runs › Fila de follow-ups | M13 | feito | 314eeeb |  |
| 16 | Runs › Reset de sessão | M10 | feito | 8801e28 | o roteiro o chama de «parcial por natureza»: run de task já começa sessão nova; o reset vale para o chat e é o contrato da continuação (#12) |
| 17 | Runs › Modelos | M11 | feito | 029e398 | sem listagem de modelos por API de provider (fora do ADR-0055) |
| 18 | Gates › Perguntas | M15 | feito | bc620ad |  |
| 19 | Gates › Plan-review | M15 | parcial | bc620ad | plano só-leitura imposto em claude e codex; em ACP ainda é possível escrever arquivo local (ADR-0041) |
| 20 | Gates › Tool call | M9 | feito | 17d9a15 | gate segue `kind=tool` (o plano dizia `tool_call`); codex sem gancho por tool nativa |
| 21 | Gates › Resubmit + audiência | M15 | feito | bc620ad | reenvio no mesmo gate vale para `approval`, `plan` e `hire` |
| 22 | Fluxos › Variáveis | M19 | feito | 76c0fb1 |  |
| 23 | Fluxos › Gatilhos/versões | M19 | feito | 76c0fb1 | gatilhos não são exportados/importados (segredos não viajam) |
| 24 | Times › Pacotes | M17 | feito | 32cdc08 | metas fora do pacote (não fazem parte do `hanko.company.v1`) |
| 25 | Empresa › Ciclo de vida | M16 | feito | 5563325 | UI de empresa arquivada só mostra a faixa; o service recusa a escrita com 409 |
| 26 | Empresa › Import | M16 | feito | 5563325 |  |
| 27 | Empresa › Decision model | M12 | feito | fb18207 | só contra `httptest`; triagem é acionada por API/CLI, não sozinha |
| 28 | Acesso | M16 | feito | 5563325 |  |
| 29 | Custos › Incidente | M15 | feito | bc620ad |  |
| 30 | Inbox | M16 | feito | 315e91c | web push só contra serviço falso; interoperabilidade FCM/Mozilla/Apple por confirmar |
| 31 | Visões | M16 | feito | 315e91c |  |
| 32 | Metas › Hierarquia | M16 | feito | 315e91c |  |
| 33 | Workspaces › Execução com Git | M10 | feito | 8801e28 | sem push (a branch vive no espelho); só testado com repositórios locais `file://` |
| 34 | Projetos › Defaults | M10 | feito | 8801e28 | política de plano do projeto veio com o M15 (`plan_policies`) |
| 35 | Ambientes remotos | M11 | feito | 029e398 | Daytona só contra servidor de teste; docker e SSH reais não exercitados |
| 36 | Skills | M17 | feito | 32cdc08 | fonte estritamente somente-leitura não adotada (o fork é o caminho) |
| 37 | Plugins | M18 | feito | 1412b00 | iframe só testado por cabeçalho/HTML, sem navegador real; ícone da aba ainda genérico |
| 38 | Integrações › Conexões | M17 | feito | 32cdc08 | OAuth só contra fake (`oauthtest`) |
| 39 | Integrações › Permissão | M9 | feito | 17d9a15 |  |
| 40 | Integrações › Gateway | M9 | parcial | 17d9a15 | endpoint único cobre só integrações MCP http; `hanko_*` e plugins seguem em `/mcp` (ADR-0053 §5) |
| 41 | Segredos | M7 | feito | e64c5d5 | cofre externo (KV v2) contra fake; segredo de usuário não é resolvido em run de persona |
| 42 | Canais externos | M19 | feito | 76c0fb1 | Telegram e GitHub só contra fakes; gates `question`/`plan`/contratação/instrução sem botão no Telegram |
| 43 | Harnesses | M12 | feito | fb18207 | fixtures de Pi/Cursor e flags de devin/gemini/opencode escritas pela documentação, sem binário real |
| 44 | Cases | Decisão | fora |  | plano: fora por ora (2026-10-16); porém Pipelines e cases já existem desde a paridade v0.3 (ADR-0034) |
| 45 | Pipelines | M20 | feito | 762a95b | entrou como «Etapas do projeto»; só contra fakes |
| 46 | Status cards | M20 | feito | 762a95b | motor decision só contra `httptest` |
| 47 | Técnico › PG embutido | Trilha Onda 2A | feito | 2d154ae | precisa de PostgreSQL instalado (nada é baixado) |
| 48 | Técnico › Realtime | Decisão | fora |  | fica só SSE (sem WebSocket) |
| 49 | Técnico › Error tracking | Trilha O1 | feito | 2a9bbd8 | painel do Grafana validado como JSON/YAML, não num Grafana rodando |
| 50 | Técnico › Storage | M7 | feito | e64c5d5 | BlobStore; S3 contra fake |
| 51 | Técnico › Rate limit | Trilha O1 | feito | 2a9bbd8 |  |
| 52 | Técnico › MCP standalone | Trilha Onda 2B | feito | PR #34 (8abab60) | testado contra servidor MCP de mentira, não contra um Hanko completo |
| 53 | Técnico › Auth | Trilha O1 | feito | 2a9bbd8 |  |
| 54 | Técnico › Distribuição | Trilha Onda 2B | feito | PR #34 (8abab60) | `install.sh` e cask nunca rodaram contra release real; exige o repo `homebrew-tap` e o secret antes da 1ª tag |
| 55 | Técnico › i18n | Decisão | fora |  | fica pt-BR + en |
| 56 | Processos › Testes | Trilha Já | feito | 93da09a | pisos de cobertura no CI (domain/heartbeat ≥80, service ≥70) |
| 57 | Processos › E2E | Trilha O1 | feito | 2a9bbd8 | 24 testes Playwright contra Postgres 16; não exercita 5xx real |
| 58 | Processos › Evals | Trilha Onda 2B | parcial | PR #34 (8abab60) | cenários e chaos só contra roteiro de mentira; nenhuma chamada a modelo real foi feita |
| 59 | Processos › CI | Trilha Já | feito | 93da09a | CI por propósito (ADR-0049) |
| 60 | Processos › Release | Trilha Já | parcial | 93da09a | goreleaser, `release.yml` e notas prontos; as tags v0.1.0/v0.3.0–v0.6.0 ainda não foram publicadas (passo do dono) |
| 61 | Processos › Announcements | Trilha Onda 3 | feito | PR #37 (14aaf9a) | «O que há de novo» lido do CHANGELOG embutido |
| 62 | Processos › Docs | Trilha Onda 3 | parcial | PR #37 (14aaf9a) | gerador do site e feature-map prontos; a hospedagem em `docs.hanko.oute.pro` ficou de fora |
| 63 | Processos › Higiene do repo | Trilha Já | feito | 93da09a | Settings de segurança ligados pelo dono em 2026-10-20; falta só «Private vulnerability reporting» |
| 64 | Operação › Instância | Trilha Onda 2A | feito | 2d154ae | imagem do prd (distroless) sem `pg_dump`; `doctor` avisa |
| 65 | Operação › Backup | Trilha Onda 2A | feito | 2d154ae | no prd a tela de backup fica desligada (sem cliente PostgreSQL na imagem); segue valendo `backup.sh` + restic |
| 66 | Operação › Settings | M7 | feito | e64c5d5 |  |
| 67 | Operação › Onboarding | Trilha Onda 3 | feito | PR #37 (14aaf9a) | jornada Playwright do assistente não escrita |
| 68 | Operação › CLI | Trilha Onda 3 | feito | PR #37 (14aaf9a) | `sandbox up` em primeiro plano coberto só por `--no-serve` |
| 69 | Operação › Navegação | Trilha Onda 3 | feito | PR #37 (14aaf9a) |  |
| 70 | Operação › Dev labs | Trilha O1 | feito | 2a9bbd8 |  |

**Total: 62/70 feito, 5 parcial, 3 fora, 0 pendente, 0 bloqueado.**

Parciais: #19 (plan-review em ACP), #40 (gateway único), #58 (evals sem modelo real), #60 (tags por publicar), #62 (site de docs não hospedado). Detalhes e ações do dono em `docs/VALIDACAO-MANHA.md`.
