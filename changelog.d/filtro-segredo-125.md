### Fonte externa: filtro de segredo no decision model (#125)

- Texto com padrão de segredo colado (chave privada, chave de API, token do GitHub, chave AWS, token do Slack, chave do Google, JWT, Bearer, URL com senha, atribuição de senha ou token) não sai do Hanko em nenhuma feature. A invocação vira erro `secret_detected`, com só o tipo do padrão no ledger e no evento `decision.invoked`, nunca o trecho (ADR-0079, item 6).
- English summary: the decision model refuses prompts that contain a secret pattern, in every feature, before calling the provider; the ledger and the event keep only the pattern kind.
