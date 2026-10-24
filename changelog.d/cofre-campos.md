### Cofre para agentes: campos, coleções e histórico (fatia 1, #56)

- Segredo com N campos: `${secret:ITEM#campo}` lê um campo; `${secret:ITEM}` continua lendo o campo `value`. Campo visível (aparece para member) ou secreto (nunca aparece na listagem).
- Coleções com concessões a persona, subárvore do organograma, todas as personas ou humano (`use`, `view`, `manage`). Na configuração (`adapter_config`) da persona, um item que ela não alcança falha a run antes do adapter, com o nome do item e da persona, nunca o valor.
- Migração 00204: cada segredo ganha o campo `value` com o mesmo material; cada empresa ganha a coleção `padrão`, concedida a todas as personas. Nada que roda hoje quebra.
- Histórico por campo com retenção de 5 versões ou 90 dias (configurável em `secrets.history_keep_versions` e `secrets.history_keep_days`); restaurar uma versão cria versão nova.
- API: criar, listar, mudar e trocar valor de campo; CRUD de coleção e segredos da coleção. Revelar campo, histórico, restaurar e conceder ou revogar concessão exigem reautenticação (#57) e seguem em 501.
- CLI `hanko secret field …` e `hanko secret collection …`; tela de campos e coleções em Configurações › Segredos.
- English summary: vault fields and collections (ADR-0068 §1–§3) — secrets with N fields, collections with use/view/manage grants, migration 00204 copies each secret into the `value` field and creates the default collection granted to all personas, per-field history with 5-version/90-day retention, and restore as a new version. Reveal, history, restore and grants wait for re-authentication (#57) and stay 501 in the API.
