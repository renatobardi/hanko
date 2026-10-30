### Fonte externa: sessão externa a partir da ingestão (fatia 1 da v0.8, #125)

- A ingestão agrupa os fatos pelo `oute.task.id` que a fonte declara e cria a sessão externa da empresa, uma vez, com
  `activity_event`. Repositório, agente e rodada da sessão seguem a hora do fato mais antigo que os trouxe (migration
  00213). Fato sem `oute.task.id` não cria sessão.
- Ainda não há conversa externa, gerador de nome, classificação, tela nem leitura.

- English summary: ingestion groups facts by the `oute.task.id` the source declares and creates the external session
  of the company once, with `activity_event`. Repository, agent and round follow the oldest fact that carried them
  (migration 00213). A fact without `oute.task.id` creates no session. No conversation, name generator, classification,
  screen or reading yet.
