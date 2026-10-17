# Plano de evolução — decisões item a item (2026-10-16)

Fonte detalhada dos itens `#N` citados em `docs/ROADMAP.md`. Em conflito, ADR vence.

| # | Item | Decisão |
|---|---|---|
| 1 | Tasks › Dependências bloqueantes | Relação N:N `task_dependencies` (sem ciclo, mesma empresa), `bloqueada` derivado, wake automático ao fechar todas, tool MCP |
| 2 | Tasks › Árvore pai/filho | Breadcrumb + lista de filhos na UI; pai acorda ao fechar o último filho; pai não conclui com filho aberto (override humano com motivo) |
| 3 | Tasks › Pausa de árvore | Hold herdado: `hold_reason`+quem/quando, vale p/ descendentes (calculado), despacho recusa, retomar acorda wakes pendentes, visível no thread e Caixa |
| 4 | Tasks › Política por task | Override de revisor e require_review por task; precedência task > projeto > persona, origem gravada no run |
| 5 | Tasks › Anexos | Anexo = artefato kind=attachment vinculado a task/comentário; upload UI (drag-drop), CLI, MCP; mesma cota/CSP; lista no briefing |
| 6 | Tasks › Rascunhos | No servidor: tabela `drafts` por usuário+empresa+alvo (task/conversa); sobrevive troca de dispositivo; limpa ao enviar |
| 7 | Personas › Instruções | `persona_instruction_revisions` append-only (autor, ator, motivo, sha256); run grava revisão usada; diff/restaurar; persona propõe mudança via gate |
| 8 | Personas › Contratação | Humano admin+ contrata direto; persona contratando gera gate `hire` com spec, aprovar cria atomicamente; política opcional por empresa exige gate p/ humanos |
| 9 | Personas › Principal e built-in | `membership.primary_persona_id` (default de task e chat); `personas.system=true` (curadoria, revisor) não removíveis, editáveis só por owner |
| 10 | Personas › Readiness | Probe por harness (binário, auth, modelo) com passos nomeados em `persona_readiness`; na contratação, botão "Testar agora" e periódico (river); falha = alerta + bloqueio de despacho com motivo |
| 11 | Chat | `/new` zera session_ref com marcador; anexos (modelo do #5); tools `hanko_project_create`/`hanko_goal_create`. Sala multi-persona: FORA (depois) |
| 12 | Runs › Continuação | Run interrompido gera continuação (retoma session_ref + briefing), máx 2 tentativas; esgotou → Caixa com diagnóstico; `continuation_of_run_id` |
| 13 | Runs › Ações do operador | Card de diagnóstico (causa, tentativa, próxima ação, dono); Tentar de novo / Continuar / Encerrar; tool de efeito externo em curso ao morrer → "desfecho incerto" exige decisão humana |
| 14 | Runs › Steer | Capability `steer` por adapter: claude (stream-json stdin) e ACP injetam; codex cai em interromper+continuar; UI mostra o modo |
| 15 | Runs › Fila de follow-ups | Fila unificada chat+task: ordem e motivo da espera; editar/descartar antes de consumida; cancelar dispara próximo run com pendentes |
| 16 | Runs › Reset de sessão | Ação "Nova sessão" na task (UI/CLI): próximo run ignora session_ref, só briefing + memória; registrado na atividade |
| 17 | Runs › Modelos | Catálogo híbrido: `model_prices` + o que o probe reporta; select com "outro"; aviso sem preço (cai no de reserva). Teste de adapter = coberto pelo #10 |
| 18 | Gates › Perguntas | Gate `kind=question` com schema (single/multi/texto, N campos); resposta estruturada no wake; dispensar/reabrir; Caixa + thread |
| 19 | Gates › Plan-review | `kind=plan` opt-in (task/projeto/persona); 1º run só planeja (sem tools de escrita), plano como artefato; aprovado → execução recebe plano; changes → replaneja |
| 20 | Gates › Tool call | Motor de política off/ask/allow por persona×tool (default empresa); ask → gate `kind=tool_call` com timeout; "sempre permitir" grava regra; ACP nativo, claude via hook PreToolUse; mesmo motor p/ integrações |
| 21 | Gates › Resubmit + audiência | `gate_revisions`: reenvio no mesmo gate com diff, revisão automática roda de novo; `gate.audience` opcional (usuários/papéis) restringe decisor e Caixa |
| 22 | Fluxos › Variáveis | `flows.variables` (nome, obrigatória, default); `{{var}}` em título/descrição; built-ins data/semana/disparo; Run now pede obrigatórias; cron exige default |
| 23 | Fluxos › Gatilhos/versões | Gatilho webhook (URL+HMAC, payload→variáveis, rate limit); gatilho por evento interno (activity_event); cron opcional; `flow_revisions` append-only + archive ≠ disabled. Override no Run now: FORA |
| 24 | Times › Pacotes | `hanko.team.v1` = subset do company.v1 (personas, skills, fluxos, projetos, metas); preview; passo de assinaturas/harness; instala pausado; catálogo embutido + arquivo/URL; CLI `teams` |
| 25 | Empresa › Ciclo de vida | Editar nome/fuso/descrição (prefixo só com zero tasks); arquivar = pausa personas, cancela runs, desliga fluxos, read-only, restaurável; excluir só owner, só arquivada, confirma nome, export automático antes |
| 26 | Empresa › Import | Dry-run/tela de prévia (contagens, avisos); destino nova ou existente; conflito por item pular/renomear/substituir; reusado por #24 |
| 27 | Empresa › Decision model | Serviço interno "decidir" por empresa, provider OpenRouter (chave como segredo da empresa, modelo escolhido do catálogo OpenRouter); features registradas (triagem, classificador de fase, nome/resumo); invocações no ledger com papel system. Fast responses: só junto com canais externos |
| 28 | Acesso | `hanko login` via navegador (/cli-auth/<código>, API key com escopo + nome do dispositivo) + `whoami` + revogar na UI; join requests (link público, admin aprova e escolhe papel); tela de usuários da instância (admin) |
| 29 | Custos › Incidente | `budget_incidents` empresa/projeto/persona; item na Caixa; resolver = subir teto / manter pausado / liberar só este run; retoma só o pausado por orçamento; audit |
| 30 | Inbox | `inbox_state` usuário×item (lido, arquivado; reaparece se muda); visão Não lidos + badge; @usuário/@persona (humano → Caixa, persona → wake); toasts SSE por usuário; web push PWA com preferências por tipo |
| 31 | Visões | "Minhas" = participação (criei, responsável, mencionado, comentei, audiência de gate); busca cobre metas, projetos, gates, artefatos, fluxos; atividade filtra por ator (API/UI/CLI) |
| 32 | Metas › Hierarquia | `goals.parent_id` (sem ciclo, prof. máx 4); progresso do pai = agregado; briefing inclui cadeia até a raiz; UI em árvore |
| 33 | Workspaces › Execução com Git | Repo do projeto → clone espelho (credencial = segredo); worktree+branch por task sob a raiz da empresa; estados provisionando/pronto/falhou/divergente; aba Git (status, diff, log) + browser read-only; limpeza ao concluir/mergear; revisa ADR-0025. Serviços (dev server): FORA. Cobre "repositório do projeto" |
| 34 | Projetos › Defaults | `projects.instructions` (briefing após as da persona); `goal_id` padrão; política de revisão/plano (#4, #19); auto-import de skills do repo com clone (#33) |
| 35 | Ambientes remotos | `environments` com drivers local, docker (container oute-agent por run), ssh (Tailscale; Mac/oMLX) + 1 sandbox cloud (e2b ou Daytona — escolher no ADR); probe real, capacidades; atribuição persona/projeto com precedência. Imagens custom: depois |
| 36 | Skills | `skill_revisions` append-only (sha256, diff, restaurar) + pin no run; `skill_sources` (repo+path+ref), sync gera revisão, fonte read-only + fork; Studio = run de teste isolado com persona descartável, custo marcado como teste. Política por empresa: FORA |
| 37 | Plugins | Executável + manifesto (capabilities pedidas), JSON-RPC stdio; contribui tools, gatilhos de fluxo, provider de ambiente (#35), fonte de skill (#36) e páginas/abas via iframe sandboxed + ponte postMessage; admin concede capabilities; enable/disable/remove; SDK Go + `hanko plugin new` |
| 38 | Integrações › Conexões | OAuth 2.1+PKCE (MCP auth spec, DCR), token cifrado + refresh, identidade da empresa ou pessoal; probe de auth + rascunho/Concluir depois/Reconectar; gate `kind=connection` com retomada da task. Catálogo de apps: FORA |
| 39 | Integrações › Permissão | Motor do #20 vale p/ tools de integração; tool nova no tools/list entra off (quarentena) + aviso na Caixa; testar tool na UI como persona X; log de chamadas por conexão (ator, duração, resultado redigido) |
| 40 | Integrações › Gateway | `/mcp` vira gateway (Hanko + integrações + plugins) aplicando #20 e logando (#39); perfis de tools reutilizáveis por persona; harness recebe 1 endpoint; token por cliente externo opcional |
| 41 | Segredos | `secrets` da empresa (AES-GCM, rekey, reveal-once) referenciados por `${secret:X}` em integrações, env da persona (corrige env sem cifra), repo (#33), OpenRouter (#27); segredos de usuário (só dono, ações em nome dele); provider de cofre externo opcional resolvendo no despacho (1º provider a escolher no ADR). Proposta via gate: FORA |
| 42 | Canais externos | Camada `channel` (entrada/saída, vínculo de identidade, anexos, estado de entrega) + drivers Telegram (conversar, decidir gates) e GitHub (PR/issue acorda persona, publica review); fast responses (#27) entram aqui; demais canais como plugins (#37) |
| 43 | Harnesses | Devin CLI como harness de 1ª classe (`devin acp` sobre o motor ACP, preset próprio na contratação, probe de auth #10, teste de contrato no CI; CLI, não o web); presets ACP com teste de contrato no CI (gemini, opencode); Pi nativo (`pi --mode json -p`, tools via CLI + run token); Cursor CLI (stream-json); harness HTTP próprio (loop de tool-use, OpenRouter/OpenAI-compat, reusa #27, destrava oMLX) |
| 44 | Cases | FORA por ora (reabrir com caso de uso real) |
| 45 | Pipelines | Estágios por projeto mapeados a status/persona; avançar acorda persona do estágio; reusa gates/revisão automática; learnings = decisões de gate → memória governada; base do AI-DLC por fase |
| 46 | Status cards | Pergunta salva + persona ou decision model (#27) + cron; resultado = artefato versionado no painel; arquivar |
| 47 | Técnico › PG embutido | `hanko serve --embedded-pg` (PG17 local) só p/ dev/trial; prd segue PG externo (ADR-0011 intacto) |
| 48 | Técnico › Realtime | Fica só SSE (sem WS) |
| 49 | Técnico › Error tracking | Via OTel: exceptions como span events + logs com stack; painel "Erros" no Grafana provisionado; erros do browser → Hanko → OTLP; sem SaaS |
| 50 | Técnico › Storage | Interface `BlobStore`: postgres (default) + s3-compat (MinIO/R2/S3); limite maior com S3; comando de migração entre drivers |
| 51 | Técnico › Rate limit | Token bucket em Postgres por API key/usuário/IP: escritas, webhooks de entrada, MCP; 429 + Retry-After; métricas OTel |
| 52 | Técnico › MCP standalone | `hanko mcp` (stdio) no mesmo binário, falando com API remota + token; sem npm |
| 53 | Técnico › Auth | First-admin claim (link de uso único no boot) substitui `create-owner` manual; OIDC genérico depois |
| 54 | Técnico › Distribuição | `install.sh` + tap Homebrew via goreleaser; com #47 = 1 comando; cloud gerenciada FORA |
| 55 | Técnico › i18n | Fica pt-BR + en |
| 56 | Processos › Testes | Cobertura mínima no CI: domain/heartbeat ≥80%, service ≥70%, api via integração; toda feature entra com teste; testes de propriedade nos motores (#1, #20, orçamento) |
| 57 | Processos › E2E | Playwright ~15 jornadas críticas contra PG real + harness fake no CI; screenshot diff das telas DS principais |
| 58 | Processos › Evals | Suíte Go: persona real (Haiku) usando tools do Hanko corretamente (gate, delegação, dependência, não vazar segredo); nightly + manual, custo marcado como teste; chaos (matar run, cair DB) no mesmo runner |
| 59 | Processos › CI | Workflows por propósito: ci (PR rápido), e2e, evals nightly, release, security (govulncheck + trivy + dependabot); drift-gates mantidos |
| 60 | Processos › Release | Publicar v0.1.0/v0.2.0 já; SemVer com canais edge (main) e stable (tag); notas do changelog.d; `hanko update` respeita canal; rollback = tag anterior + restore |
| 61 | Processos › Announcements | "O que há de novo" do CHANGELOG da versão instalada (sem internet), dispensável por usuário; canal de skills = fonte GitHub (#36) |
| 62 | Processos › Docs | Site estático em docs.hanko.oute.pro a partir de docs/ + docs/en; feature-map próprio por domínio (roteiro de e2e/evals); catálogo do DS em /design-guide |
| 63 | Processos › Higiene do repo | SECURITY.md + dependabot (gomod, npm, actions). CODEOWNERS e templates: FORA |
| 64 | Operação › Instância | `hanko doctor` (PG, migrations, OTel, ai-memory, harnesses, segredos, disco); `hanko update --channel` com backup antes; `hanko service install` (systemd/launchd); deploy-prd.sh passa a usá-los |
| 65 | Operação › Backup | `hanko backup create/list/restore` (pg_dump + blobs/S3) + tela admin com histórico e agendamento (fluxo de sistema); restic do host segue; drill no CI mantido |
| 66 | Operação › Settings | `instance_settings` no banco p/ toggles não sensíveis (ACP, stdio, cotas, experimentais) com audit; env vence e trava na UI; segredos só env |
| 67 | Operação › Onboarding | Wizard retomável: empresa → assinatura/harness (probe #10) → 1ª persona ou pacote de time (#24) → 1ª task rodando; `hanko onboard` equivalente na CLI |
| 68 | Operação › CLI | `hanko context add/use/list` (gravado pelo `hanko login` #28) + instância descartável por branch/worktree para test-drive |
| 69 | Operação › Navegação | Favoritos + recentes na sidebar (preferências no servidor); perfil próprio (nome, avatar, fuso, idioma). Ordem da sidebar: FORA |
| 70 | Operação › Dev labs | `/design-guide` (Kubo DS claro/escuro, base do screenshot diff #57) + ux-labs com cenários montados (thread longo, run 10k linhas) — rotas só admin/dev |
