# ADR-0017 — Modo trusted: premissas, limites e mitigações do v0.1
Data: 2026-10-13 · Status: aceita (decisão do dono em 2026-10-14: `authenticated` sempre — ver seção "Decisão")

## Contexto
O modo `trusted` (ADR-0007) não autentica humanos: quem alcança o Hanko age como operador. O Hanko também executa harnesses
de agente (`claude`, `codex`) como processos filhos, no mesmo host e sob o mesmo usuário do servidor.

## Premissa
Em `trusted`, **agentes e operador compartilham o host**. A fronteira de confiança é o host, não o processo.

## Limites conhecidos (não mitigáveis dentro do processo)
- Um agente local pode **omitir o run token** e chamar a API/UI em loopback como se fosse o humano (ator `human`), inclusive
  criar empresas, contratar personas, criar workspaces e cancelar runs. Hoje isso é indistinguível de uma chamada do operador.
- Um agente com acesso ao sistema de arquivos pode ler o que o usuário do servidor lê (`.env`, `hanko.yaml`, credenciais do host).
- Um agente com rede pode alcançar qualquer serviço acessível a partir do host (Postgres, ai-memory).

## Mitigações do v0.1 (reduzem acidente e vazamento, não impedem agente malicioso local)
1. **Host check** (421): bloqueia DNS rebinding contra o navegador do operador.
2. **Allowlist de ambiente** do harness: segredos do servidor (`HANKO_SECRET`, `HANKO_DATABASE_URL`, `HANKO_MEMORY_TOKEN`, AWS etc.) não são herdados.
3. **Redação de segredos** em tudo que vira `run_events` (valores exatos + padrões de credencial).
4. **Proxy de memória** `/memory-mcp`: o agente usa o run token; o token do ai-memory e o escopo ficam no servidor.
5. **Run token vinculado ao run ativo**: vale só com o run `queued`/`running`; persona (token) não administra empresa, personas, workspaces, projetos nem cancela runs.
6. **Workspaces** confinados a `HANKO_WORKSPACES_ROOT`; `HANKO_SECRET` mínimo de 32 bytes; borda com `basic_auth` até o M5.

## Recomendação
- Rodar os agentes **isolados do servidor**: container/LXC separado (runtime próprio), sem acesso ao `.env`/banco do Hanko, alcançando
  apenas `HANKO_PUBLIC_URL` (API/MCP/`/memory-mcp`) e o que a persona precisa; ou
- **Antecipar o modo `authenticated`** (Firebase, ADR-0014) do M5, de modo que a API exija sessão humana e o ator `human` deixe de ser o padrão de loopback.

## Decisão pendente (dono)
Escolher entre isolar os agentes (custo operacional, sem mudança de código) e antecipar `authenticated` (custo de produto, remove o limite principal).
Até lá, tratar o v0.1 como adequado a uma máquina de um único operador confiável e a agentes que não sejam adversariais.

## Adendo (M5): o modo `authenticated` existe
O modo `authenticated` (ADR-0007, ADR-0014) está implementado: UI exige sessão, `/api/v1/*` exige sessão, API key de usuário ou run token,
e `/mcp` exige run token ou API key. Nele, **agentes locais que omitiam o token passam a receber 401** e o ator `human` deixa de ser
o padrão do loopback: o ator humano é sempre um usuário identificado. Ligue-o (docs/RUNBOOK.md §5) para remover o limite principal deste ADR;
os demais limites (sistema de arquivos e rede do host) continuam valendo e a recomendação de isolar agentes se mantém.

## Decisão 2026-10-14 (dono)
`HANKO_MODE=authenticated` sempre — inclusive em dev local. É o padrão da config, do `.env.example` e do compose. `trusted` só por escolha explícita, para teste descartável, e o boot emite aviso. Provider padrão do deploy: `firebase` (projeto `oute-sso`).
