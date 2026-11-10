# ADR-0093 — Backup do prd sem dump do banco
Data: 2026-11-10 · Status: aceita (Bardi, 2026-11-10) · Altera: ADR-0092 (item 4)

## Contexto
Com o banco no Postgres compartilhado do lab (ADR-0092), o `backup.sh` diário do LXC seguia fazendo o dump do schema
`hanko_prd`, por um contêiner descartável da imagem `postgres:17` mantida no LXC só para isso. O banco já entra no dump
semanal do servidor compartilhado, que o lab leva ao restic e ao offsite.

## Decisão
1. Com banco externo, o `backup.sh` não faz dump do banco: guarda só a memória (ai-memory) e os artefatos em
   `/var/backups/hanko`, que o restic semanal copia.
2. O `restore.sh` com banco externo repõe só memória e artefatos e recusa `--pgdump`; o banco volta pelo backup do
   servidor compartilhado (`pg_restore -n hanko_prd` do dump semanal).
3. Os scripts deixam de precisar de cliente do Postgres no modo externo; a imagem `postgres:17` sai do LXC.
4. Com Postgres próprio (profile `localdb`) nada muda.
5. O `hanko backup` interno continua escopado ao schema corrente (ADR-0092 item 3), para quem roda com cliente do Postgres.

## Consequências
- Ponto de recuperação do banco do prd passa de diário a semanal (até 7 dias de perda num restore), o mesmo dos outros
  apps do servidor compartilhado.
- O backup pré-deploy deixa de guardar o banco: voltar uma migration ruim é pelo dump semanal ou por correção para frente
  (migrations são append-only).
- Menos peças no LXC: sem imagem extra, sem conexão ao banco no backup.

## Conceitos rejeitados
- **Cliente do Postgres 17 na imagem do Hanko:** manteria o dump diário, ao custo de ~20 MB e de um repositório de pacotes a
  mais no build; o dono preferiu a peça a menos.
