### Backup do prd sem dump do banco (ADR-0093)

- Com banco externo, o `backup.sh` guarda só a memória e os artefatos, e o `restore.sh` repõe só esses dois e recusa `--pgdump`: o banco é coberto pelo backup do banco compartilhado. Os scripts não usam mais cliente do Postgres nesse modo, e a imagem `postgres:17` sai do LXC. Sem nenhum arquivo para somar, o `backup.sh` não roda mais `sha256sum` sem argumentos.

- English summary: with an external database, `backup.sh` keeps only the memory and attachment archives and `restore.sh` restores only those and refuses `--pgdump`; the database is covered by the shared database's backup. The scripts no longer use a Postgres client in that mode, and the `postgres:17` image leaves the LXC. With nothing to checksum, `backup.sh` no longer runs `sha256sum` without arguments.
