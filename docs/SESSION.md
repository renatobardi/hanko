# Estado da última sessão (anexos de 64 MiB, driver fs e cota de 5 GiB, ADR-0065)
Data: 2026-10-23 · branch `feat/anexos-64mib` → `main`

## Feito
- Driver `fs` do BlobStore (`HANKO_BLOB_DRIVER=fs`, `HANKO_BLOB_DIR`); migration 00202 aceita `fs` em `artifacts.storage`.
- Limite por arquivo = teto do driver sem configuração (64 MiB fs/s3, 16 MiB postgres); envio: 10 arquivos e 128 MiB; cota padrão 5 GiB.
- Prd: volume `blobs` em `/var/lib/hanko/blobs`, deploy migra postgres→fs, backup.sh/restore.sh cobrem `blobs-<UTC>.tar`; vhost nginx 130m.
## Travado (só o dono)
- Deploy com `--edge` para o nginx novo valer: `deploy/oute-server/deploy-prd.sh --edge`.
## Próximo passo
- PR 2: anexos em disco para a persona (Read do claude lê imagem e PDF), em vez de base64 pelo MCP.
