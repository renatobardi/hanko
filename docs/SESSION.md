# Cofre, fatia 6: marca de senha em campo (#61) · branch `feat/61-cofre-marca-senha`
Data: 2026-10-25 · sessão `61-cofre-saude-extras` (rodada swarm-1024-0625)

## Feito
- Marca explícita de senha em campo (ADR-0074, migration 00206): `password` criado por `POST …/fields` e alterado por `PATCH …/fields/{field}` e pela CLI (`--password`, `--no-password`). O relatório de fracos lê a marca além do nome.

## Travado
- Prazo de rotação por campo na UI (item b do Falta): decisão do Bardi, por BLOQUEADO.
- Container sem Go nem sqlc: o CI do PR valida o código gerado e o build.

## Próximo passo
- Aplicar o diff de código gerado do CI e conferir os testes; PR com `Refs #61`.

# Cofre, fatia 5: delegação de segredo pessoal, uso da empresa e testes (#60)
Data: 2026-10-25 · branch `feat/60-cofre-delegacao-extras` · sessão `60-cofre-delegacao-extras` (rodada swarm-1024-0625)

## Feito
- Delegação só vale com o alvo aberto (task não fechada, conversa não arquivada), ADR-0068 §6.3.
- `GET .../secrets/{name}/usage` ligado: admin, ou membro com `view`/`manage` numa coleção que contém o segredo.
- `delegation.expired` não é emitido: motivo no ADR-0068 §6, item 6.
- Testes novos: alvo conversa, task fechada, uso da empresa (service); resolve no executor (heartbeat); 4 rotas de delegação e o uso da empresa (API, integração).

## Travado
- Nada. O container não tem Go: a validação é o CI do PR.

## Próximo passo
- Conferir o CI do PR; o merge fica com o dispatcher.

# Cofre, fatia 1: ações sensíveis ligadas à reautenticação (#56)
Data: 2026-10-24 · branch `feat/56-cofre-ligar-501` · sessão `56-cofre-ligar-501` (rodada swarm-1024-0625)

## Feito
- 12 operações saíram do 501 (revelar campo, histórico listar/revelar/restaurar, apagar campo, em `secrets` e `user-secrets`; criar/revogar concessão). Reautenticação exigida no service (`requireFieldSensitive`, `requireGrantAction`); teste do gate no service e da API no teste de integração de campos.
- CI do PR é a validação: o container não tem Go.
- CI reprovou duas vezes e foi corrigido: 67a0afa (teste do histórico, linha 86: v2 gerada já conta como revelada) e 74a26d2 (revogar concessão já revogada: `n == 0` virava 204; agora 404 em concessão e em apagar campo). Head 2425856: `ci`, `security`, `e2e` verdes.
- Fora deste PR: o mesmo `n == 0` em `DeleteSecretCollection` e na remoção de membro de coleção (vault.go, ~941 e ~987).

## Próximo passo
- Merge pelo dispatcher. Nada pendente nesta issue além do `## Falta` do PR #86.

# Estado da última sessão (corte da v0.7.0)
Data: 2026-10-24 · branch `release/v0.7.0` · sessão `release-v0-7-0` (sem issue, rodada swarm-1024-0625)

## Feito
- Corte da v0.7.0 (docs/RELEASING.md passos 1–3): seção `## [0.7.0] — 2026-10-25` no CHANGELOG.md com as 8 fatias (#55–#62), `[Unreleased]` vazio, fragmentos de `changelog.d/` removidos; `scripts/release-notes.sh notes 0.7.0` imprime a seção.
- Commit assinado `release: v0.7.0`; PR do ramo `release/v0.7.0` para a main. Main verde antes do corte (ci e security em 4c3d86b). Sem tag, sem merge, sem deploy (passos 4–6, do Bardi).

## Travado
- Nada.

## Próximo passo
- Bardi: fazer merge, taguear `v0.7.0` no commit mergeado e deployar no `hanko-prd` (canal de aprovação).

# Sessão #58 (cofre, fatia 3: build) · rodada swarm-1024-0625 · branch `feat/58-cofre-arquivo-build`
Data: 2026-10-24 · issue #58 · `Refs #58`
## Feito
- Varredura de `hanko-secret-*` no boot (ADR-0073, proposta), rekey de campo arquivo em páginas, migração de campos arquivo entre drivers (`hanko blobs migrate`), purga dos arquivos pessoais na saída, CLI `secret field add --kind file --file`.
## Travado
- Sem Go no container: nada foi compilado nem testado aqui; o CI é a primeira validação.
## Próximo passo
- Ver o CI do PR; aceite do Bardi no ADR-0073. Sandbox para `${secretfile:}` e o `qa` ficam no `## Falta` do PR.
