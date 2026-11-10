# Banco do prd no Postgres compartilhado · fechamento (ADR-0092)
Data: 2026-11-09

## Feito
- Prd migrado para o schema `hanko_prd` (#217): ensaio + cutover com 204 tabelas / 18.713 linhas iguais por tabela, deploy `ab3469e3`, teste do dono ok.
- Credencial `app_hanko_prd` no `.env` do LXC e no cofre (hash conferido). Container `hanko-postgres-1` e volume `hanko_pgdata` removidos; backup semanal do lab passou a copiar só `/var/backups/hanko`.
- Docs e scripts sem citar o produto do banco compartilhado (regra do AGENTS.md).

## Travado
- Nada.

## Próximo passo
- Conferir no log do backup semanal a linha `files hanko from hanko-prd` sem `pg_dump hanko`.
