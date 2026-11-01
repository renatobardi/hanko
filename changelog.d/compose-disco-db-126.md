### Compose de produção mede o disco do Postgres

- O `docker-compose.yml` monta o volume `pgdata` só para leitura em `/pgdata-disk` no contêiner do Hanko e define `HANKO_DB_DISK_PATH`, o que liga o alerta `db_disk_low`. O Hanko só consulta o espaço livre do disco; não lê nem escreve nos dados do banco. Para desligar, tire a variável e a linha do volume (`docs/RUNBOOK.md`).

- English summary: the production `docker-compose.yml` mounts the `pgdata` volume read-only at `/pgdata-disk` in the Hanko container and sets `HANKO_DB_DISK_PATH`, which turns on the `db_disk_low` alert. Hanko only queries the disk's free space; it neither reads nor writes the database files. To turn it off, remove the variable and the volume line (`docs/RUNBOOK.md`).
