# Kaizen #94: geradores antes do push e número de migration no AGENTS.md
Data: 2026-10-25 · sessão `94-kaizen-hanko-agents` (kaizen da rodada swarm-1024-0625)

## Feito
- `AGENTS.md` ("Regras de trabalho") com as duas regras da #94: rodar antes do push os geradores que o job `gerados` confere (`make openapi`, `make generate`, `make islands`) e commitar o resultado; e, ao criar migration e ao atualizar o branch com a `origin/main`, conferir o maior número em `db/migrations/` da `origin/main` e usar o seguinte.
- Conferido: o `AGENTS.md` não tem teto de tamanho (busca por `AGENTS` no repo inteiro: nenhuma regra de tamanho). Nenhuma regra saiu ou fundiu.

## Travado
- Nada.

## Próximo passo
- Dispatcher/Bardi: fazer merge do PR da #94.
