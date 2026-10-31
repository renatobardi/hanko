# Cofre (#117) · sessão `feat-117-campo-arquivo`
Data: 2026-10-30

## Feito
- Troca do campo arquivo pela tela arquiva a versão anterior em `secret_field_versions` e sobe a versão (ADR-0068 §3.1); restaurar uma versão de arquivo a põe como versão nova (§3.3). Migration `00215_cofre_versao_arquivo.sql`: o arquivo arquivado fica no postgres (`secret_field_version_blobs`) ou no fs e s3 sob a chave do id da versão.
- Apagar o campo, a poda do histórico e a saída do membro apagam os arquivos arquivados; migração entre drivers e rekey de `HANKO_SECRET_KEY` cobrem também as versões.
- `origin/main` (25a8e13) incorporada. Testes de banco passam com Postgres local (service, storage, ui, api, db).
- Sonar: S3776 em `DeleteSecretField`, `RevealSecretFieldVersion` e `PruneSecretFieldHistory` corrigidos (5096b3b, 6a2d42e). Abertos fora deste PR, de funções que não mudaram: `UpdateSecretFieldMeta`, `RotateSecretField`, `RevealSecretField`, `rekeySecrets`, `sealPersonaEnv`, `CreateSecretDelegation`, `ResolveRunUserSecrets`, `prepareMaterialAAD`, `purgeMemberSecrets`.

## Travado
- Critério `qa` (d) da #117 (demo em produção): fica com o Bardi. Por isso o PR segue `Refs #117`.
- Os testes com banco rodaram só localmente (Postgres 18.6 arm64); o CI roda com Postgres 17.

## Próximo passo
- Reauditar o head do PR #146 e confirmar CI e Sonar nesse head. O merge é do Bardi.
