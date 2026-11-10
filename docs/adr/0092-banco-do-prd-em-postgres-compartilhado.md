# ADR-0092 — Banco do prd num Postgres compartilhado, schema `hanko_prd`
Data: 2026-11-08 · Status: aceita (Bardi, 2026-11-08) · Altera: ADR-0008, ADR-0062, ADR-0083 · Usa: ADR-0011

## Contexto
O prd roda o Postgres 17 num contêiner próprio do LXC `hanko-prd` (volume `pgdata`, ~1,1 GB), com backup local
(`backup.sh`) e o `pg_dump` semanal do lab. O lab tem um Postgres 17 compartilhado no LXC `oute-supabase-db`
(`10.173.117.51:5433`), com um schema por projeto: o papel `app_<schema>` é dono do seu schema, tem o `search_path`
fixo nele, não cria banco nem schema e não enxerga `public` nem os schemas dos outros. Dois apps já migraram para ele, e o
schema `hanko_prd` (papéis `app_hanko_prd` e `ro_hanko_prd`) já existe. O dump semanal do lab cobre o banco inteiro.

Três peças do Hanko assumem um banco só dele:
- o backup interno (`hanko backup`, ADR-0062) faz `pg_dump` do banco inteiro e o restore roda
  `DROP SCHEMA public CASCADE; CREATE SCHEMA public`, que o papel não pode executar e que, com um superusuário, apagaria
  dados de outros projetos;
- o compose monta o volume do banco no Hanko para o alerta `db_disk_low` (ADR-0083) e monta a URL do banco a partir de
  `POSTGRES_PASSWORD`;
- `deploy-prd.sh`, `backup.sh` e `restore.sh` falam com o banco por `docker compose exec postgres`.

## Decisão
1. **O prd usa o schema `hanko_prd` do Postgres compartilhado.** Só muda a `HANKO_DATABASE_URL`, que fica no `.env` do LXC
   e vem do cofre (item `hanko-prd HANKO_DATABASE_URL`). O Hanko não muda de código para isso: goose, river e as consultas
   usam o `search_path` do papel. O Postgres continua sendo a única dependência (ADR-0011).
2. **O compose lê a URL do `.env`.** O serviço `postgres` passa para o profile opcional `localdb` (`depends_on` com
   `required: false`). O `deploy-prd.sh` escreve no `.env` uma URL para `@postgres` quando não há nenhuma (instalação nova ou
   anterior a este ADR) e liga o profile só nesse caso.
3. **O backup interno cobre só o schema corrente.** `pg_dump --schema=<current_schema()>`; o manifest grava `db_schema`
   (vazio = `public`, os backups antigos). O restore recusa backup de outro schema, esvazia o schema corrente sem apagá-lo
   (tabelas, views, sequências, funções, tipos, domínios, configurações de busca e collations) e recria o conteúdo do dump
   sem a entrada do schema, tudo numa transação do `psql`. Com banco próprio o schema é `public` e nada muda para o operador.
4. **Os scripts do prd têm dois modos**, decididos pela URL do `.env` (`deploy/oute-server/pglib.sh`): local (`@postgres`,
   como antes) e externo. No externo os clientes rodam num contêiner descartável `postgres:17` (`HANKO_PG_CLIENT_IMAGE`) com a
   conexão só em `PG*` do ambiente, nunca em argv; o backup sai do schema corrente; o pré-deploy sempre faz backup; o restore
   guarda o estado atual em `pre-restore-<ts>.pgdump` e aplica com o mesmo SQL do item 3 (`clear-schema.sql`, conferido
   contra o Go por teste) numa transação; o `--check` só confere o dump, porque não há banco descartável.
5. **`db_disk_low` não é avaliado no prd.** O disco do banco não é do LXC do Hanko; o compose deixa de montar o volume e de
   definir `HANKO_DB_DISK_PATH`. O alerta continua disponível para quem tem Postgres próprio e define a variável.
6. **A migração dos dados** é feita pela ferramenta do banco compartilhado (`ops/migrate_pg_schema.sh` no repo dele), com
   ensaio e contagem por tabela, não pelo restore do Hanko.

## Consequências
- Um backup a menos para cuidar no LXC: o schema entra no dump semanal do banco compartilhado, e o `backup.sh` diário segue
  como cópia rápida local.
- Disponibilidade do Hanko passa a depender também do LXC do banco compartilhado.
- O restore com banco externo não guarda o schema anterior lado a lado como o local fazia (renomear o banco); a garantia é o
  dump `pre-restore-<ts>` e a transação única.
- O advisory lock das migrations (goose) passa a ser do banco compartilhado: outro cliente com o mesmo id de lock esperaria
  as migrations do Hanko. Nenhum dos clientes atuais usa goose.

## Conceitos rejeitados
- **Um banco por projeto no servidor compartilhado:** o console do servidor só gerencia um banco; o servidor já fixou um
  schema por projeto.
- **Restore renomeando o schema:** exigiria `CREATE` no banco para o papel, que só os tenants de vários schemas têm.
- **Medir o disco do servidor compartilhado pelo Hanko:** o alerta passaria a falar de outro sistema; o monitoramento do
  lab cobre o host.
