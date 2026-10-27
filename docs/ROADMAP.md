# Roadmap

## v0.1

**Status 2026-10-15:** M0–M5 entregues e em produção (https://hanko.oute.pro). Falta só o gate humano do processo: demos gravadas e tags `v0.1.0-mN`/`v0.1.0`. Parte do v0.2 já entrou (abaixo). Ver `docs/SESSION.md`.

**Capacidade declarada: 60 h/semana (40 h dias úteis + 20 h fim de semana).** Meta: **6 semanas** (5 de entrega + 1 de folga). Gargalo deixa de ser hora de código e passa a ser o gate humano (revisão, decisão, demo) — por isso a folga.

Alavancas (ADR-0009): contrato antes de código → 3 trilhas paralelas de agentes; incremento executável + commit por sessão (`AGENTS.md`); demo semanal; ≥70% cobertura só em domain/heartbeat.

Regras de parada:
- Milestone estoura 2x → corta escopo, nunca estica prazo.
- M1 não roda na semana 1 → para e reavalia.
- Pós-M2: UI >50% das horas → reavalia ADR-0005.
- Duas semanas seguidas abaixo de 40 h reais → replaneja para 8 semanas (não força ritmo).

| M | Semana | Entregável | Demo |
|---|---|---|---|
| M1 ✅ | 1 | Contrato (migrations com vocabulário ADR-0016 + `event_id`, `occurred_at/received_at`, `actor_type`, `memory_outbox`) → company, persona (adapter `cli`), task, heartbeat (river), run, log SSE, comentário. API huma + CLI. OTel do 1º request. | `hanko serve` → contratar persona → criar task → run → log ao vivo → comentário. Trace no Grafana. |
| M2 ✅ | 2 | UI host (Kubo DS): painel, tasks/board, task + thread, execuções ao vivo, contratar persona. MCP server. **ai-memory ligado**: personas (MCP+hooks) e Hanko (outbox + briefing no despacho). | Run 2 no mesmo workspace recebe handoff do run 1. Dogfood começa. |
| M3 ✅ | 3 | Organograma (reports_to, delegação, checkout atômico) + Gates (audit trail, Caixa de entrada/Decisões). | Persona delega; humano decide gate; decisão vira memória. |
| M4 ✅ | 4 | Custos e orçamentos (ledger, hard stop, assinaturas) + Fluxos (river periodic). | Orçamento estoura → run bloqueado. Fluxo diário cria task sozinho. |
| M5 ✅ | 5 | Auth firebase/local; deploy `hanko-prd` (pg17 + ai-memory + LGTM; borda nginx do host), backup/restore, smoke, docs en, UI pt-BR/en, CHANGELOG. | hanko.oute.pro com login Google. |
| — | 6 | Folga: estouro, bugs do dogfood, polish. | — |

## v0.2
Entregue em 2026-10-15 (antes do planejado):
- Assinaturas, quatro custos, trava semanal, origem do modelo (ADR-0019).
- Alertas operacionais, nome amigável de run, revisão automática por outro modelo (ADR-0020).
- Multi-humano: papéis por empresa (owner/admin/member/viewer), convites, autorização no service (ADR-0021).
- Troca de empresa na UI; formulário do revisor; UI bilíngue (pt-BR/en).
- `hanko export` / `hanko import` de empresa (ADR-0022).
- Busca única da empresa: tela Buscar, API, CLI e tool MCP, full-text do Postgres e memória (ADR-0024).

- Consolidação de memória governada: curadoria periódica como run de sistema, propostas com gate humano, permissões por workspace (ADR-0030).

- ACP: harness genérico por stdio (JSON-RPC), liberado pelo operador com `HANKO_ACP_ALLOW` (ADR-0023).
- Ilhas React: log de run virtualizado, organograma interativo, editor ancorado de comentários (ADR-0026).
- Projetos e workspaces com raiz por empresa e orçamento de projeto (ADR-0025); chat direto com persona (ADR-0027).
- Artefatos versionados e metas (ADR-0028); skills e integrações MCP/webhook (ADR-0029).
- PWA e CLI em inglês.
- Quatro revisões independentes e as correções de todos os achados.

Pendente: tag `v0.1.0`/`v0.2.0` e demos.

## v0.3 → v0.6 — plano de evolução (decidido em 2026-10-16)

Plano único decidido item a item pelo dono em 2026-10-16 (70 itens; 8 ficaram fora, listados no fim). Os números `#N` são os itens dessa rodada. Cada marco segue a regra do v0.1: **contrato antes de código** (migrations + tipos de domínio + OpenAPI), ADR antes do contrato quando muda modelo de dados, demo gravada no fim.

**Já entregue no v0.3 (ADR-0031):** UI alinhada ao Kubo DS, glifo por persona, tema, quadro de tasks arrastável e visões salvas, projeto e workspace em abas, timeline, painel ao vivo, ambientes/adapters/plugins (leitura), export/import pela UI, times prontos e troca da própria senha.

**Entregue depois do plano (ADR-0064, migration 00201):** documentos de artefato com identidade estável e versão MAJOR.MINOR (persona declara major/minor, servidor confere pelo diff), restaurar, reclassificar e tags; a persona lista, lê e restaura artefatos pelo MCP e os vê no briefing.

**Também entregue no v0.3, antes deste plano (ADR-0032 a 0038, migrations 00027–00034):** cards de status; sala com várias personas; pipelines, cases, fila de revisão e aprendizados; versões, fontes e estúdio de skills; catálogo de tools, perfis e gateways; ambientes SSH e sandbox; plugins de terceiros. Consequência: os números de ADR 0032–0048 previstos abaixo já estão ocupados; os ADRs do plano seguem a partir do 0039, e os marcos M11 (ambientes), M17/M18 (skills, plugins) e M20 (pipelines) partem do que já existe. Itens que o plano listava como "fora" (sala, cases) foram entregues por decisão posterior do dono.

**Ordem de execução (por dependência, sem datas).** As «semanas» do v0.1 eram estimativa de capacidade humana; neste plano o único freio são os gates (testes verdes, ADR, contrato, smoke). Grafo:

- Raiz paralela: trilha contínua (todas as ondas), M7, M13, M14.
- M8, M9, M10, M11, M12 e M16 dependem de M7.
- M15 depende de M8 e M9. M17 depende de M7 e M9. M18 depende de M11 e M17. M19 depende de M7 e M16. M20 depende de M12 e M15.
- Releases: `v0.3.0` ao fechar a Onda 1 (M7–M9), `v0.4.0` na 2 (M10–M13), `v0.5.0` na 3 (M14–M16), `v0.6.0` na 4 (M17–M20).
- Regras de parada: corte de escopo segue a ordem inversa das ondas (Ecossistema antes de Governança antes de Execução); fatia que falha duas correções é revertida e anotada em `docs/VALIDACAO-MANHA.md`. Andamento por item em `docs/PROGRESSO.md`.

### Trilha contínua — qualidade e operação (paralela a todas as ondas)
Começa junto com M7 e roda em paralelo a todas as ondas.

| Fase | Itens |
|---|---|
| Já | **#60** publicar `v0.1.0` e `v0.2.0`; canais edge (main) e stable (tag), notas geradas de `changelog.d`. **#63** `SECURITY.md` + dependabot (gomod, npm, actions). **#59** CI separado por propósito: `ci` (PR rápido), `e2e`, `evals` (nightly), `release`, `security` (govulncheck + trivy); gates de drift mantidos. **#56** cobertura mínima no CI: domain/heartbeat ≥ 80 %, service ≥ 70 %, api por integração; toda feature entra com teste; testes de propriedade nos motores (#1, #20, orçamento). |
| Onda 1 | **#53** first-admin claim (link de uso único no boot; substitui `auth create-owner`). **#51** rate limit geral (token bucket em Postgres por API key, usuário e IP; 429 + `Retry-After`; métricas). **#49** erros via OTel: exceptions como span events, painel "Erros" provisionado, erros do browser → Hanko → OTLP. **#57** Playwright com ~15 jornadas críticas contra PG real + harness fake; screenshot diff das telas principais. **#70** `/design-guide` (DS em claro e escuro, base do screenshot diff) + ux-labs com cenários montados (thread longo, run com 10 mil linhas); só admin/dev. |
| Onda 2 | **#64** `hanko doctor`, `hanko update --channel` (backup antes) e `hanko service install` (systemd/launchd); `deploy-prd.sh` passa a usar os três. **#65** `hanko backup create/list/restore` (pg_dump + blobs) + tela admin com histórico e agendamento por fluxo de sistema; restic do host continua; drill no CI mantido. **#47** `hanko serve --embedded-pg` só para dev/trial. **#54** `install.sh` + tap Homebrew via goreleaser. **#52** `hanko mcp` (stdio) falando com a API remota + token. **#58** evals de comportamento: persona real (Haiku) usando as tools do Hanko corretamente (gate, delegação, dependência, não vazar segredo), nightly + manual, custo marcado como teste; chaos (matar run, derrubar DB) no mesmo runner. |
| Onda 3 | **#67** onboarding: wizard retomável (empresa → assinatura/harness com probe → 1ª persona ou pacote de time → 1ª task rodando) + `hanko onboard`. **#68** `hanko context add/use/list` (gravado pelo `hanko login`) + instância descartável por branch/worktree para test-drive. **#69** favoritos e recentes na sidebar (preferências no servidor) + perfil próprio (nome, avatar, fuso, idioma). **#61** "O que há de novo" lido do CHANGELOG da versão instalada (sem rede), dispensável por usuário. **#62** site de docs estático em `docs.hanko.oute.pro` (`docs/` + `docs/en`) + feature-map por domínio, que serve de roteiro para e2e e evals. |

### Onda 1 — Fundações (v0.3)
| M | Entregável | ADR | Demo |
|---|---|---|---|
| M7 | **#41** segredos como recurso da empresa (AES-GCM, rekey, reveal-once), referenciados por `${secret:X}` em integrações, env da persona (corrige o env sem cifra), credencial de repo e provider de modelo; segredos de usuário (visíveis só ao dono); provider de cofre externo opcional resolvendo no despacho. **#66** `instance_settings` para toggles não sensíveis, com audit; env vence e trava na UI. **#50** `BlobStore` com drivers postgres (padrão) e s3-compat, mais comando de migração entre drivers. | 0032 segredos · 0033 BlobStore | Persona usa um segredo por referência; o env antigo migra cifrado; artefato vai para S3. |
| M8 | Revisões append-only padronizadas: **#7** instruções da persona (autor, motivo, sha256; o run grava a revisão usada; diff e restaurar; persona propõe mudança via gate) e base reusada por skills, fluxos e gates. **#5** anexos como artefato `kind=attachment` em task e comentário, com upload por UI, CLI e MCP. **#6** rascunhos no servidor por usuário + alvo. **#11** `/new` no chat, anexos no chat e tools `hanko_project_create` / `hanko_goal_create`. | 0034 revisões append-only | Mudança de instrução aparece com diff; o run mostra a revisão usada; anexo arrastado chega no briefing. |
| M9 | **#20** motor de política off/ask/allow por persona × tool (padrão por empresa); `ask` vira gate `kind=tool_call` com timeout; "sempre permitir" grava regra; ACP nativo, claude via hook PreToolUse. **#39** quarentena de tool nova e log de chamadas por conexão; testar tool na UI. **#40** `/mcp` vira gateway (Hanko + integrações + plugins), com perfis de tools por persona e token por cliente externo. | 0035 política de tools e gateway | Persona tenta uma tool em `ask`; humano aprova "sempre"; segunda chamada passa direto; tudo no log da conexão. |

### Onda 2 — Execução (v0.4)
| M | Entregável | ADR | Demo |
|---|---|---|---|
| M10 | **#33** workspace de execução com Git: clone espelho do repo do projeto (credencial = segredo), worktree + branch por task, estados (provisionando/pronto/falhou/divergente), aba Git (status, diff, log) e browser read-only, limpeza ao concluir ou mergear. **#34** defaults de projeto: instruções, meta padrão, política de revisão/plano, auto-import de skills do repo. **#16** "Nova sessão" na task. | 0036 workspace com Git (revisa ADR-0025) | Task cria a branch `hnk-123`, persona commita, a aba Git mostra o diff e o worktree some ao concluir. |
| M11 | **#35** ambientes: drivers local, docker (container endurecido por run), ssh (Tailscale) e 1 sandbox cloud (provider escolhido no ADR, após medir); probe real; atribuição por persona e projeto com precedência. **#10** readiness da persona (probe por harness com passos nomeados, botão "Testar agora", checagem periódica, alerta e bloqueio de despacho). **#17** catálogo híbrido de modelos. | 0037 ambientes de execução | Mesma persona roda local, em container e via SSH; auth quebrada aparece antes do run. |
| M12 | **#27** decision model por empresa: classificador externo via OpenRouter (chave como segredo, modelo do catálogo), features registradas (triagem, classificador de fase, nome/resumo), invocações no ledger como `system`. **#43** harnesses: Devin CLI de primeira classe (`devin acp`, preset próprio, probe, teste de contrato), presets ACP testados (gemini, opencode), Pi nativo (`pi --mode json -p`, tools via CLI + run token), Cursor CLI (stream-json) e harness HTTP próprio (loop de tool-use em endpoint OpenAI-compat, reusa o provider do decision model; reabre o teste com oMLX). | 0038 decision model e harness HTTP | Devin, Pi e uma persona HTTP rodam a mesma task; a triagem usa o decision model e aparece em Custos. |
| M13 | Resiliência de run. **#12** continuação limitada (retoma `session_ref` + briefing, máx. 2 tentativas, `continuation_of_run_id`). **#13** card de diagnóstico; Tentar de novo / Continuar / Encerrar; "desfecho incerto" exige decisão humana. **#14** steer onde o harness suporta (claude stream-json, ACP); codex cai em interromper+continuar. **#15** fila de follow-ups unificada (chat + task), editável, com cancelar disparando o próximo run. | 0039 recuperação e steering | Matar o processo no meio: o run continua sozinho; mensagem injetada muda o rumo do run ativo. |

### Onda 3 — Governança (v0.5)
| M | Entregável | ADR | Demo |
|---|---|---|---|
| M14 | Coordenação de tasks. **#1** dependências N:N sem ciclo, `bloqueada` derivado, wake ao fechar as dependências, tool MCP. **#2** árvore pai/filho na UI, pai acorda ao fechar o último filho e não conclui com filho aberto (override com motivo). **#3** hold herdado por subárvore. **#4** override de revisor por task (task > projeto > persona, origem gravada). | 0040 coordenação de tasks | A persona quebra a task em três com dependência; elas destravam em cascata; pausar o pai congela tudo. |
| M15 | Gates. **#18** `kind=question` com schema (single, multi, texto, N campos), dispensar e reabrir. **#19** `kind=plan` opt-in (1º run só planeja; aprovado → execução recebe o plano). **#21** resubmit no mesmo gate com diff + audiência nomeada. **#8** contratação por persona via gate que cria a persona. **#9** persona principal por usuário + personas de sistema protegidas. **#29** incidentes de orçamento (empresa/projeto/persona) com resolução. | 0041 gates estendidos | Persona pergunta via formulário, depois submete plano; humano pede mudança; reenvio no mesmo gate com diff. |
| M16 | Caixa e visões. **#30** lido/arquivado por pessoa, Não lidos + badge, @usuário/@persona, toasts SSE, web push no PWA. **#31** "Minhas" por participação, busca cobrindo metas/projetos/gates/artefatos/fluxos, atividade por ator. **#32** árvore de metas com rollup. **#25** editar, arquivar e excluir empresa. **#26** import com dry-run e merge seletivo. **#28** `hanko login` pelo navegador + `whoami`, join requests, tela de usuários da instância. | 0042 caixa por pessoa e notificações | @menção chega como push no celular; arquivar limpa a Caixa sem fechar a task. |

### Onda 4 — Ecossistema (v0.6)
| M | Entregável | ADR | Demo |
|---|---|---|---|
| M17 | **#38** conexões: OAuth 2.1 + PKCE para MCP remoto (spec de autorização do MCP, registro dinâmico), refresh, identidade da empresa ou pessoal; probe de auth e setup recuperável; gate `kind=connection` que retoma a task. **#36** skills: fontes (repo + path + ref) com sync e fork, revisões com pin no run, Studio de teste isolado. **#24** pacotes de time `hanko.team.v1` (subset do company.v1, preview, credenciais exigidas, instala pausado, CLI `teams`). | 0043 conexões OAuth · 0044 fontes de skills | A persona pede uma conexão; o humano autoriza OAuth; a task retoma; um time instalado de pacote roda. |
| M18 | **#37** plugins: executável + manifesto com capabilities, JSON-RPC por stdio; contribuem tools, gatilhos de fluxo, provider de ambiente, fonte de skill e páginas/abas em iframe sandboxed com ponte postMessage; admin concede capabilities; SDK Go + `hanko plugin new`. | 0045 plugins | Plugin de exemplo instalado adiciona uma tool e uma aba; revogar a capability tira as duas. |
| M19 | **#42** canais: camada `channel` (entrada/saída, vínculo de identidade, anexos, estado de entrega) com drivers Telegram (conversar e decidir gates) e GitHub (comentário em PR/issue acorda a persona, publica review); respostas rápidas do decision model entram aqui; outros canais como plugin. **#22** variáveis de fluxo (`{{var}}`, obrigatórias, built-ins). **#23** gatilhos por webhook (HMAC) e por evento interno, cron opcional, revisões e arquivar. | 0046 canais externos · 0047 gatilhos de fluxo | Gate decidido pelo Telegram; comentário num PR acorda a persona; webhook dispara fluxo com variáveis. |
| M20 | **#45** pipelines sobre tasks: estágios por projeto mapeados a status e persona, avanço acorda a persona do estágio, reusa gates e revisão automática, learnings = decisões de gate em memória governada (base do workflow por fase). **#46** status cards: pergunta salva + persona ou decision model + cron, resultado como artefato versionado no painel. | 0048 pipelines e status cards | Task atravessa triagem → dev → review → QA sozinha, parando só nos gates; card de status atualiza de manhã. |

### Fora deste plano (decisão de 2026-10-16)
Sala com várias personas; override de projeto/workspace no "Run now"; catálogo curado de apps; proposta de segredo via gate; Cases; política de skills por empresa; CODEOWNERS e templates de issue/PR; reordenar a sidebar; WebSocket (fica só SSE); i18n além de pt-BR e en; serviços de workspace (dev server); imagens custom de sandbox; OIDC genérico; cloud gerenciada.

## v0.7 — Cofre para agentes (decidido em 2026-10-24, #54)

Decisões da entrevista de intent em #54; ADR-0068 (cofre), ADR-0069 (envio) e ADR-0070 (reautenticação e passkey).
Regra de parada: se estourar, corta-se na ordem inversa das fatias (7, depois 6, depois 5).

| Fatia | Issue | Entregável | Depende de |
|---|---|---|---|
| 0 | #55 | ADRs + contrato (migration 00203, tipos de domínio, queries, 54 operações da API em 501) | — |
| 1 | #56 | Campos, coleções, migração para a coleção `padrão`, histórico e restaurar | 0 |
| 2 | #57 | Reautenticação recente e passkey | 0 |
| 3 | #58 | `hanko_secret_get`, TOTP e `${secretfile:}` | 1 |
| 4 | #59 | Envio com chave no fragmento | 1, 2 |
| 5 | #60 | Delegação de segredo pessoal e saída do humano | 1, 3 |
| 6 | #61 | Gerador, relatório de saúde e alerta de rotação | 1 |
| 7 | #62 | Import de `.env` e export cifrado | 1, 2 |

## v0.8 — agent-studio no Hanko (decidido em 2026-10-24, #66)

Decisões da entrevista de intent em #66; ADR-0075 a 0086 e ADR-0088. O objetivo é desligar o agent-studio sem perder
informação: portar todas as capacidades, migrar todos os dados e reapontar o oute e o Tray (Hanko Desktop). A v0.7 já
fechou (#54 a #62), então o contrato pode começar (#66, decisão 12).
Regra de ordem: as fatias seguem a dependência dos fatos. **Nenhuma fatia sai do plano:** a #66 manda portar todas as
capacidades, então a ordem decide só a entrega, sem corte de trás para frente.
Antes do ensaio (fatia 6), a #81 (tokens gravados nos spans internos do Codex) precisa estar decidida.

| Fatia | Issue | Entregável | Depende de | Demo |
|---|---|---|---|---|
| 0 | a abrir | Contrato: migrations (a partir da maior da `origin/main`), tipos de domínio, queries e OpenAPI em 501, com `source_kind` em cada conceito (ADR-0086) e o alerta de disco do Postgres (comentário da #66) | v0.7 fechada | Os geradores passam e a API nova responde 501 em cada operação |
| 1 | a abrir | Fonte e ingestão: agente externo, emparelhamento, host, listener `ingest`, OTLP, conversa e sessão, classificação e gerador único de nome (ADR-0075 a 0079 e 0084) | 0 | Um host manda OTLP à ingestão e a conversa aparece com nome, classe e repositório emparelhado |
| 2 | a abrir | Custo elevado e alertas de agente externo (ADR-0082 e 0083) | 1 | A chamada externa aparece com custo e classe; um alerta abre e fecha |
| 3 | a abrir | Rodada externa e aprovação externa (ADR-0080 e 0081) | 1, 2 | Uma rodada do swarm e um pedido do canal aparecem; a tela mostra `oute approve <id>` |
| 4 | a abrir | Telas, Caixa, busca e export dos dados externos (ADR-0088) | 1, 2, 3 | Os conceitos aparecem nas telas, na Caixa e na busca; o export leva só a configuração |
| 5 | a abrir | Hanko Desktop (ADR-0085) | 4 | O app mostra a Caixa e abre o Terminal no `oute approve` |
| 6 | a abrir | Migração, ensaio e conciliação 100% contra uma cópia do DuckDB (ADR-0082 item 8) | 1, 2, 3, 4 | Cada diferença do ensaio tem explicação, e as somas de custo batem |
| 7 | a abrir | PRs dos consumidores no oute-agent e janela de corte, com volta garantida (#66, decisões 2, 3, 11 e 13) | 5, 6 | O oute manda e lê pelo Hanko; o agent-studio fica parado e intacto até o ok do Bardi |

## Depois (ideias, sem data)
- Modelo local no Mac (oMLX) por persona: harness `claude` com `adapter_config.env` (`ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_MODEL`) apontando ao endpoint Anthropic do oMLX; não suportado oficialmente pela Anthropic para modelos não-Claude. **Testado em 2026-10-15 (Mac 24 GB, oMLX 0.7.0 via Tailscale): inviável por ora.** Rede, chave e `/v1/messages` funcionam a partir do container do Hanko, mas Qwen2.5-Coder-14B e Qwen3-8B-4bit devolvem a chamada de tool como texto (sem `tool_use`), e o Qwen3.8-27B não cabe na RAM com o Mac em uso. Persona operacional simples fica no Haiku; reavaliar com mais RAM ou com um modelo de tool calling comprovado no oMLX (o harness HTTP do M12 reabre o teste). Se testar de novo: guard de memória `aggressive`, `sudo purge` antes e `--tools Bash` para encolher o prompt.
