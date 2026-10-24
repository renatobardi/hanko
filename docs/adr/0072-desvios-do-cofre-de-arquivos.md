# ADR-0072 — Desvios da fatia 3 do cofre: pasta temporária, blob em tabela e sandbox recusado
Data: 2026-10-24 · Status: aceita (2026-10-24) · Issue: #58 (parte de #54) · PR: #80 · Altera: ADR-0068 §5.1, §5.2 e §5.3

## Contexto
A fatia 3 do cofre (#58) entregou o campo arquivo e a referência `${secretfile:ITEM#campo}`. Na implementação, três
pontos saíram do que o ADR-0068 fixa para a entrega. O PR os declarou em "Desvios do desenho", mas nenhum foi registrado
em `docs/adr/`, e o `AGENTS.md` pede ADR novo para decisão tomada no meio da implementação. A auditoria do PR #80
(comentário <https://github.com/renatobardi/hanko/pull/80#issuecomment-6390492916>, achado 1) apontou isso como bloqueio.

Os três desvios:

1. **Pasta do `${secretfile:}`.** O ADR-0068 §5.2 pede "um diretório `0700` do workspace da run". A implementação cria o
   diretório com `os.MkdirTemp("", "hanko-secret-*")`, em `/tmp` (`internal/service/secrets_files.go:45@cfe4a78`). O
   workspace da run fica dentro da worktree do git: um `git add -A` do agente pegaria o arquivo com o segredo em claro.
2. **Blob do campo arquivo no driver postgres.** O ADR-0068 §5.1 pede que o blob cifrado vá ao `BlobStore` (ADR-0051).
   No driver postgres ele vai para a tabela própria `secret_field_blobs` (migração `00205_cofre_arquivos.sql`), e não
   para `artifact_blobs`, que tem chave estrangeira para artefatos. Os drivers fs e s3 usam o `BlobStore` como previsto
   (`internal/service/vault_files.go:27@cfe4a78`).
3. **Sandbox recusado.** O ADR-0068 §5.3 e o escopo da #58 incluem o ambiente sandbox (ADR-0037). A implementação
   recusa `${secretfile:}` em sandbox e em cloud, com a mensagem `arquivo de segredo ainda não é suportado em ambiente
   <sandbox|cloud>` (`internal/heartbeat/executor.go:706@cfe4a78`). O motivo é que o caminho do arquivo não chega ao
   sandbox montado, e a montagem não foi resolvida nesta fatia.

## Decisão
Aceitam-se os três desvios como decisão da fatia 3, com as consequências abaixo. O ADR-0068 continua valendo no resto.

1. **Pasta temporária, fora da worktree.** O `${secretfile:}` cria o arquivo `0600` num diretório `0700` de
   `os.MkdirTemp`, fora da worktree e fora do workspace da run. O diretório sai com `RemoveAll` ao fim da run, inclusive
   quando ela falha.
2. **Blob do driver postgres em `secret_field_blobs`.** A tabela tem chave estrangeira para `secret_fields` com
   `ON DELETE CASCADE` e um `CHECK` de 2 MiB no conteúdo cifrado. O conteúdo continua cifrado com o mesmo AAD do campo.
   Os drivers fs e s3 seguem o `BlobStore`.
3. **Sandbox e cloud recusam `${secretfile:}`** na hora da run, com erro claro, como o SSH já fazia.

## Consequências
- **Pasta temporária.** Se o processo morrer no meio da run (kill -9, falta de memória), o arquivo fica na pasta
  `hanko-secret-*` sem prazo. A pasta é `0700`, mas aparece na listagem de `/tmp` para qualquer processo do mesmo usuário.
  Pendência registrada: varrer as pastas `hanko-secret-*` antigas no boot. Ela não entra nesta fatia, porque apagar uma
  pasta de outro processo em uso derrubaria a run dele, e o limite de idade ainda não foi definido.
- **Tabela de blobs.** Trocar o driver de blob (de postgres para fs ou s3) não tem caminho de migração para os campos
  arquivo. O `hanko blobs migrate` cobre só artefatos. O rekey de chave (`RekeyIntegrationSecrets`) recifra os blobs nos
  dois modos. Pendência registrada: migração de campos arquivo entre drivers.
- **Sandbox e cloud.** Uma persona que roda em sandbox ou cloud não consegue usar `${secretfile:}`, e a falha aparece na
  run, não ao salvar. Pendência registrada: suporte a sandbox em outra entrega, com ADR próprio para a montagem.
- **O que não muda.** Campo arquivo continua cifrado antes de qualquer driver; o arquivo em claro existe só enquanto a run
  roda, com permissão `0600`; `${secretfile:}` só vale no `adapter_config` da persona (commit `59cb284`).

## Alternativas descartadas
- **Mover a pasta para o workspace da run**, como o ADR-0068 §5.2 pede. Descartada: a worktree do git pode receber um
  `git add -A` com o segredo em claro.
- **Guardar o blob em `artifact_blobs`.** Descartada: a chave estrangeira aponta para artefatos.
- **Suportar sandbox já nesta fatia.** Descartada: a montagem do arquivo não estava resolvida, e a fatia já tinha os
  critérios `build` cobertos por teste.
