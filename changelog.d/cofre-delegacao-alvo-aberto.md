### Cofre para agentes: delegação só com o alvo aberto e uso do segredo da empresa (fatia 5, #60)

- Delegação de segredo pessoal deixa de valer enquanto a task está fechada (feita ou cancelada) ou a conversa arquivada; reaberta, volta a valer (ADR-0068 §6.3).
- `GET /companies/{company}/secrets/{name}/usage` responde: onde e quando um segredo da empresa foi usado. Vê o admin e o membro com `view` ou `manage` numa coleção que contém o segredo.
- O fato `delegation.expired` não é emitido; o motivo está no ADR-0068 §6.
- Sem migration.
- English summary: a personal-secret delegation stops working while its task is closed (done or cancelled) or its conversation is archived, and works again when reopened (ADR-0068 §6.3). `GET /companies/{company}/secrets/{name}/usage` now lists where and when a company secret was used; admins and members with `view` or `manage` on a collection that holds the secret can see it. The `delegation.expired` fact is not emitted; ADR-0068 §6 says why. No migration.
