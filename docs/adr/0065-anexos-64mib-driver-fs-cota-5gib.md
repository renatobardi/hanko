# ADR-0065 — Anexos de até 64 MiB, envio de até 128 MiB, driver fs em disco e cota de 5 GiB
Data: 2026-10-22 · Status: aceita · Altera: ADR-0028 (cota padrão), ADR-0051 (drivers e limite padrão por arquivo), ADR-0052 (limite por envio)

## Contexto
O dono anexou uma imagem de 3,3 MiB num chat e quis padronizar arquivos maiores em todo o Hanko. Os limites estavam
desencontrados:

- **Por arquivo:** padrão 5 MiB (`HANKO_ARTIFACT_MAX_BYTES`), com teto de 16 MiB no driver postgres e 64 MiB no s3
  (ADR-0051).
- **Por envio:** 10 arquivos × o limite por arquivo. Não havia teto próprio para o envio, então a memória de uma
  requisição crescia com o limite por arquivo.
- **Na borda:** o vhost nginx de produção cortava em 20 MiB (`client_max_body_size 20m`), abaixo dos 50 MiB que o Hanko
  aceitava por envio. Um envio de 5 arquivos de 5 MiB recebia 413 do nginx.
- **Cota:** 512 MiB por empresa, o que cabia poucos arquivos grandes.
- **Onde guardar:** arquivo grande não cabe bem no Postgres (`bytea` materializa o valor inteiro e engorda o
  `pg_dump`). O dono quer os arquivos no próprio oute-server, sem conta em provedor externo. O MinIO, o S3 self-hosted
  mais conhecido, deixou de ter edição comunitária mantida, e qualquer S3 self-hosted traria mais um serviço e um
  certificado (o driver s3 exige HTTPS fora de loopback).

O dono decidiu, em 2026-10-22: 64 MiB por arquivo, no máximo 10 arquivos e 128 MiB por envio, arquivos no disco do
LXC, nginx compatível e cota de 5 GiB por empresa.

## Decisão
1. **Driver `fs` do BlobStore.** Terceiro driver, ao lado de postgres e s3: arquivos em `<HANKO_BLOB_DIR>/<empresa>/<artefato>`
   (diretório 0700, arquivo 0600). Grava num temporário do mesmo diretório, faz `fsync` e renomeia, então um leitor
   nunca vê arquivo pela metade. Como no s3, não é transacional: o arquivo é gravado antes da transação do artefato,
   com a mesma compensação. As chaves são UUIDs, então o caminho nunca sai da raiz. `artifacts.storage` aceita `fs`
   (migration 00202), `hanko blobs migrate` move de e para ele, e o backup de blobs externos o inclui. Ativo com
   `HANKO_BLOB_DRIVER=fs` e `HANKO_BLOB_DIR` (absoluto); com `HANKO_BLOB_DIR` definido e outro driver ativo, ele fica
   registrado só para leitura e migração.
2. **Limite por arquivo = teto do driver ativo, quando não configurado.** Sem `HANKO_ARTIFACT_MAX_BYTES`, o limite passa
   a ser o teto do driver: **64 MiB com fs ou s3**, 16 MiB com postgres (o teto do ADR-0051 continua). Definir a
   variável continua valendo, de 1 byte ao teto do driver. A mesma configuração serve a qualquer instalação sem
   recusar o boot.
3. **Envio: no máximo 10 arquivos e 128 MiB somados.** A UI (chat, sala, comentário e cartão de anexos da task) limita
   o corpo a `min(10 × limite por arquivo, 128 MiB)` mais a folga do multipart e confere a soma dos arquivos. O erro diz
   o teto e pede para mandar em partes. A dica do campo mostra os três números.
4. **Cota padrão por empresa: 5 GiB** (`HANKO_ARTIFACT_QUOTA_BYTES`; antes 512 MiB). Continua configurável e somando
   todas as versões.
5. **Produção (`hanko-prd`) usa o driver fs** num volume docker próprio (`blobs`, montado em `/var/lib/hanko/blobs`).
   O `deploy-prd.sh` move o que ainda está no Postgres com `hanko blobs migrate --from postgres --to fs` (idempotente;
   cada versão é conferida por tamanho e sha256 antes de trocar de driver). O `backup.sh` arquiva o volume
   (`blobs-<UTC>.tar`, depois do dump, sem os temporários) junto com o dump e o ai-memory, e o `restore.sh` o repõe
   (`--blobs`); o restic do lab segue coletando `/var/backups/hanko`.
6. **Borda:** o vhost de `deploy-prd.sh --edge` aceita 130 MiB (`client_max_body_size 130m`), espera até 5 min por um
   corpo lento (`client_body_timeout 300s`) e entrega o corpo direto ao Hanko (`proxy_request_buffering off`), que impõe
   os limites.

## Alternativas descartadas
- **S3 num provedor (R2, AWS):** o dono quer os arquivos no oute-server.
- **S3 self-hosted (Garage, SeaweedFS, MinIO):** mais um serviço para manter e um certificado para o endpoint, para
  guardar arquivos na mesma máquina onde o Hanko já roda. Continua possível com o driver s3, se um dia os arquivos
  precisarem ficar fora do LXC.

## Consequências
- Um envio no teto ocupa até ~128 MiB de memória enquanto é lido (o corpo continua em memória, como no ADR-0051). Com
  4 GiB no LXC, isso cabe com folga para alguns envios simultâneos. Ler e gravar em fluxo (`io.Reader` até o
  `BlobStore`) fica para quando o limite passar de 64 MiB.
- O disco do LXC passa a crescer com os arquivos (até a cota de cada empresa), e o backup local guarda até
  `HANKO_BACKUP_KEEP` cópias do volume. O `hanko doctor` já avisa com pouco disco.
- Instalações sem configuração sobem de 5 MiB para 16 MiB por arquivo; quem quer o valor antigo define
  `HANKO_ARTIFACT_MAX_BYTES`.
- O teste `TestDeployPrdVhostFitsTheUploadBudget` impede que o vhost volte a cortar abaixo do envio máximo.
- **Fora daqui:** a persona ler arquivos grandes. A tool `hanko_attachment_get` devolve binário só até 1 MiB e em base64,
  que o modelo não enxerga como imagem nem PDF. Entregar os anexos em disco para a persona é a decisão seguinte.
