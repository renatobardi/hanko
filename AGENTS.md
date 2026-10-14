# AGENTS.md — protocolo de sessão

Este arquivo é a memória de trabalho do projeto. Toda sessão (humano, Claude Code, Cowork ou agente do próprio Hanko) começa aqui.

## Cold start (obrigatório, nesta ordem)
1. `docs/ROADMAP.md` — qual milestone está aberto e o critério de demo.
2. `docs/adr/` — decisões vigentes. ADR vence STACK.md em conflito. Não reabrir decisão sem ADR novo.
3. `docs/STACK.md` — stack e convenções.
4. `git log --oneline -20` e issues abertas do milestone.
5. `docs/SESSION.md` — último estado deixado pela sessão anterior (o que ficou pronto, o que travou, próximo passo).

## Fim de sessão (obrigatório)
- Toda sessão termina com algo executável + commit (`-s`). Sem exceção. Zero sessões "só refatorando".
- Atualizar `docs/SESSION.md`: feito / travado / próximo passo em ≤10 linhas. Sobrescrever, não acumular.
- Decisão de arquitetura tomada no meio → ADR novo em `docs/adr/NNNN-*.md`.

## Regras de trabalho
- Milestone tem contrato antes de código: migrations + tipos de domínio + OpenAPI. Trilhas paralelas só depois do contrato.
- Camadas handler → service → store. Handler não toca DB. Store não tem regra. Domain não importa infra.
- Toda escrita de domínio gera `activity_event` na mesma transação.
- Nunca citar produtos de terceiros como referência nos artefatos do projeto.
- Nunca `panic` fora de `main`; nunca `log.Fatal` fora de `cmd/`.
