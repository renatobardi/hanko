# Banco do prd no Postgres compartilhado · ADR-0092
Data: 2026-11-08

## Feito
- `hanko backup` escopado ao schema corrente (`--schema`, `db_schema` no manifest); restore esvazia o schema (`clear-schema.sql`) e recria numa transação, recusa outro schema.
- `docker-compose.yml`: URL do `.env` com fallback para o `postgres` local, que foi para o profile `localdb`; sem volume do banco nem `HANKO_DB_DISK_PATH` (`db_disk_low` desligado no prd).
- `deploy-prd.sh` liga `localdb` só sem URL externa; pré-deploy sempre faz backup com banco externo. `backup.sh`/`restore.sh` com modo externo (`pglib.sh`, cliente `postgres:17` descartável, senha só em `PG*`).

## Travado
- Nada no código; a migração dos dados depende do `ops/migrate_pg_schema.sh` (repo oute-supabase-db) e do cutover pelo Bardi.

## Próximo passo
- Ensaio → cutover → URL no `.env` do hanko-prd e no cofre → `deploy-prd.sh` → teste do dono → remover container/volume `pgdata` e atualizar catálogo/docs.
