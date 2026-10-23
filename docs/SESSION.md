# Estado da última sessão (anexos de 64 MiB com driver fs, ADR-0065; anexos em disco para a persona, ADR-0066)
Data: 2026-10-23 · branches `feat/anexos-64mib` (#43) e `feat/anexos-em-disco` (#44, sobre a primeira)

## Feito
- Driver `fs` do BlobStore (`HANKO_BLOB_DRIVER=fs`, `HANKO_BLOB_DIR`); migration 00202 aceita `fs` em `artifacts.storage`.
- Limite por arquivo = teto do driver sem configuração (64 MiB fs/s3, 16 MiB postgres); envio: 10 arquivos e 128 MiB; cota padrão 5 GiB.
- Prd: volume `blobs` em `/var/lib/hanko/blobs`, deploy migra postgres→fs, backup.sh/restore.sh cobrem `blobs-<UTC>.tar`; vhost nginx 130m.
- Anexos em `.hanko-anexos/<run>/` no diretório de trabalho, com o caminho no briefing (ADR-0066).
- Antes, no `main` (#42): doctor checa o ai-memory pelo provider; `update --check` não sugere downgrade; cask sem o hook depreciado.
## Travado (só o dono)
- Deploy com `--edge` para o nginx novo valer: `deploy/oute-server/deploy-prd.sh --edge`. Tags v0.3.0–v0.6.0; SonarCloud falhando em todo PR.
## Próximo passo
- Merge do #43 e depois do #44; deploy com `--edge`; anexar uma imagem num chat e pedir à persona para descrevê-la.
