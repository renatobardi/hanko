# Cofre (#117) · sessão `feat-117-campo-arquivo`
Data: 2026-10-30

## Feito
- Campo arquivo se troca pela tela (PR #146): "Trocar arquivo" sobrescreve o conteúdo, mantém nome e id, e não põe o conteúdo na página nem na URL (`ReplaceSecretFieldFile`, `internal/service/vault_files.go`).
- Achados SHOULD-FIX da auditoria tratados: a troca confere a linha afetada no postgres (cria se faltar); a cópia antiga sai dos outros drivers e a linha do postgres sai quando outro driver está ativo; mensagem de `RotateSecretField` corrigida; fragmento de changelog duplicado removido.
- Testes: UI sem banco (3), validação sem banco (`fileSize`, `checkFileContent`); com banco (4) em `internal/service/vault_replace_file_test.go` e `vault_replace_file_drivers_test.go`, que pulam aqui.
- `golangci-lint run ./...`: 0 issues. Geradores e testes dos pacotes tocados: ok.

## Travado
- BLOQUEADO #117: ADR-0068 §3.1 e §3.3 mandam arquivar a versão anterior em `secret_field_versions` e subir a versão. O modelo atual não guarda um blob por versão: `secret_field_blobs` tem `field_id` como chave primária (`db/migrations/00205_cofre_arquivos.sql`). Arquivar exige mudar esse modelo, o que está fora do previsto. Não decidido, segundo a decisão do Bardi.
- Os 4 testes com banco pulam aqui (sem Postgres); rodam no CI.

## Próximo passo
- Bardi decide o modelo do arquivo de versão (nova tabela ou chave com versão). Com essa decisão, arquivar e subir a versão na troca.
