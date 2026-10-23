### Anexos maiores: 64 MiB por arquivo, 10 arquivos e 128 MiB por envio, cota de 5 GiB (ADR-0065)
- Driver `fs` do BlobStore: artefatos e anexos em arquivos de um diretório local (`HANKO_BLOB_DRIVER=fs`, `HANKO_BLOB_DIR`), sem banco nem S3. O `hanko-prd` passa a usá-lo num volume próprio; o deploy migra o que está no Postgres, e `backup.sh`/`restore.sh` cobrem o volume.
- Sem `HANKO_ARTIFACT_MAX_BYTES`, o limite por arquivo passa a ser o teto do driver de blobs: 64 MiB com fs ou S3, 16 MiB com Postgres (antes 5 MiB).
- Um envio (chat, sala, comentário, cartão de anexos) leva no máximo 10 arquivos e 128 MiB somados; a dica do campo mostra os limites e o erro pede para mandar em partes.
- Cota padrão por empresa: 5 GiB (antes 512 MiB).
- vhost nginx do `deploy-prd.sh --edge`: aceita 130 MiB por pedido (antes 20 MiB, abaixo do envio máximo), 5 min para corpo lento e sem buffer no proxy.
- English summary: new `fs` blob driver (local directory) used by production; attachments default to the blob driver's ceiling (64 MiB on fs/S3, 16 MiB on Postgres), uploads cap at 10 files / 128 MiB, the default company quota is 5 GiB, and the production nginx vhost accepts the full upload.
