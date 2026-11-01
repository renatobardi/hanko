# ai-memory 3.3.1 no Hanko (#200) · sessão `hanko-ai-memory-update`
Data: 2026-11-01

## Feito
- `hanko-prd` trocado de 3.0.0 para 3.3.1, com backup antes (`/var/backups/hanko/aimemory-20261101T215320Z.tar.gz`) e `.env.pre-aim331` no LXC. Ensaio com cópia do backup antes da troca.
- Digest fixado no repo; `ReadPage` pede 64.000 caracteres e devolve o corpo; `memory_patch_page` e `memory_restore_page` retidas; teste de contrato opcional `TestContractRealServer`.

## Travado
- Falha de isolamento no proxy `/memory-mcp`, anterior à 3.3.1: tratar em privado (`SECURITY.md`), sem issue pública.

## Próximo passo
- Fazer merge do PR e deploy do Hanko: até lá o diff do gate em prd usa o cliente antigo, com corte em 12.000 caracteres.
