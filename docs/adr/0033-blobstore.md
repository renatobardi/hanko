# ADR-0033 — BlobStore: Postgres por padrão, S3 compatível opcional
Data: 2026-10-17 · Status: aceita

## Contexto
O ADR-0028 guardou o conteúdo dos artefatos em `bytea` (`artifact_blobs`): um backup só, isolamento por empresa, nenhum
diretório extra. O custo foi um limite baixo por arquivo (padrão 5 MiB, teto 16 MiB) e um dump que cresce com os
arquivos. Quem tem um armazenamento de objetos (S3, MinIO, R2) quer arquivos maiores fora do banco. A decisão de
dependências do ADR-0011 continua: Postgres é a única dependência **obrigatória**.

## Decisão
1. **Interface** `storage.BlobStore` (`internal/storage`): `Name()`, `Transactional()`, `Put(ctx, key, data, contentType)`,
   `Get(ctx, key)`, `Delete(ctx, key)`, `Stat(ctx, key)` (tamanho; a migração confere a cópia com ele). A chave é (empresa, artefato). Dois drivers: `postgres` e `s3`.
2. **Driver postgres (padrão)** é o comportamento atual: grava em `artifact_blobs` **dentro da transação** do artefato
   (`Transactional() == true`; a transação viaja no contexto, `storage.WithTx`). Nada muda para quem não configura nada.
3. **Driver s3** (qualquer endpoint S3 compatível): cliente mínimo com **SigV4 próprio sobre a stdlib**
   (`crypto/hmac`, `crypto/sha256`, `net/http`), só `PutObject`, `GetObject`, `DeleteObject` e `HeadObject`, endereçamento
   por caminho (`https://endpoint/bucket/chave`, padrão) ou por host virtual. **Não** se adiciona `aws-sdk-go-v2`:
   são dezenas de módulos transitivos para quatro chamadas, contra ~250 linhas testáveis; o assinador é conferido com o
   vetor de teste publicado pela documentação do SigV4 e por um S3 falso (`httptest`) que reassina e compara. Chave do
   objeto: `<prefixo>/<empresa>/<artefato>`. Corpo em memória (limite de arquivo ≤ 64 MiB), `x-amz-content-sha256`
   com o hash real. Credenciais **só por env** (segredo, ADR-0032): `HANKO_S3_ENDPOINT`, `HANKO_S3_REGION` (padrão
   `us-east-1`), `HANKO_S3_BUCKET`, `HANKO_S3_ACCESS_KEY`, `HANKO_S3_SECRET_KEY`, `HANKO_S3_PREFIX`,
   `HANKO_S3_PATH_STYLE` (padrão verdadeiro). Timeout de 60 s por chamada; HTTPS exigido fora de loopback.
4. **Escolha do driver ativo** por `HANKO_BLOB_DRIVER` (`postgres` | `s3`; padrão `postgres`), que decide onde as
   escritas **novas** vão. O artefato guarda **onde está** (`artifacts.storage`, migration 00032, padrão `postgres`),
   então leitura, diff, export e exclusão funcionam com os dois drivers convivendo (inclusive durante a migração). Os
   drivers configurados ficam sempre registrados: um artefato em `s3` continua legível se o driver ativo voltar a
   `postgres`, desde que as credenciais do S3 sigam no env.
5. **Escrita no S3 fora da transação.** O id do artefato é gerado antes; o objeto sobe **antes** de abrir a transação
   (não se segura a trava de cota da empresa durante um upload de rede). Se a transação falha, não cria versão (mesmo
   conteúdo) ou estoura a cota, o objeto é apagado (compensação; falha ao apagar vira log e objeto órfão, ver riscos).
   A exclusão apaga as linhas na transação e os objetos depois do commit.
6. **Limite maior com S3.** O limite por arquivo configurável sobe de 16 MiB para **64 MiB** quando o driver ativo é
   `s3` (`HANKO_ARTIFACT_MAX_BYTES`; a validação da configuração conhece o driver e a cota é do mesmo `settings`). O
   corpo da API (base64) e o do MCP acompanham o limite efetivo.
7. **Migração entre drivers.** `hanko blobs migrate --from postgres --to s3 [--company X] [--keep-source] [--dry-run]
   [--batch N]` roda no processo (precisa do banco e do env do S3), em lotes, **retomável e idempotente**: para cada
   artefato do driver de origem lê o conteúdo, confere o sha256 gravado, escreve no destino, confere ao reler (`HEAD`
   com tamanho) e só então troca `artifacts.storage` e, sem `--keep-source`, apaga a origem. Falha em um artefato é
   contada e o comando segue; o código de saída é diferente de zero se algo falhou. Vale nos dois sentidos.
8. **Export/import.** O export lê o conteúdo pelo driver de cada artefato (o snapshot do Postgres só cobre os
   `postgres`); o formato `hanko.company.v1` não muda, e o import grava no driver ativo.
9. **Telemetria.** Span `hanko.blob.<op>` (driver, tamanho, resultado) e as métricas `hanko.blob.ops` e
   `hanko.blob.bytes`.

## Consequências
- Quem não define nada continua com o comportamento do ADR-0028, byte a byte.
- Com S3, o backup do Postgres deixa de cobrir os arquivos: o backup do bucket é responsabilidade do operador (o
  RUNBOOK diz isso). O comando de migração permite voltar para o banco.
- Objeto órfão (processo morto entre o upload e a transação, ou falha ao apagar) ocupa espaço sem aparecer na cota; um
  `hanko blobs gc` fica para depois, já que a chave é determinística e dá para comparar com o banco.
- Apagar uma empresa remove as linhas por cascata, não os objetos do S3 (mesma observação).
- Presigned URLs, multipart e streaming ficam fora: o corpo cabe em memória (≤ 64 MiB) e a entrega continua passando
  por `artifactsrv`, que aplica os cabeçalhos de segurança do ADR-0028.
