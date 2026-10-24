# Estado da última sessão (cofre, fatia 1, #56)
Data: 2026-10-24 · branch `feat/56-cofre-campos` · issue #56 (parte de #54)

## Feito
- Campos de segredo (N por item, visível ou secreto), histórico por campo com retenção (5 versões ou 90 dias) e restaurar como versão nova, coleções com concessões, migração 00204.
- API (campos e coleções), CLI `hanko secret field|collection`, UI de campos e coleções, job `secret_history_tick`.
- Testes: service (critérios de build), API de integração, migração, UI e CLI.

## Travado
- Revelar campo, histórico, restaurar e conceder ou revogar concessão exigem reautenticação (ADR-0070, #57): seguem em 501 na API.

## Próximo passo
- Fatia 2 (#57): fechar a reautenticação e ligar essas 7 operações da API ao service já pronto.
