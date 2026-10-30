### Fonte externa: agentes e hosts externos, listar, renomear, arquivar, sempre ligado e juntar (fatia 1 da v0.8, #125)

- As rotas de agente e de host externos deixam de responder 501: listar, ver, renomear, arquivar ou voltar, marcar
  "sempre ligado" (só host) e juntar dois agentes ou dois hosts. Alterar e juntar são de owner ou admin. Cada alteração
  gera evento de atividade. O nome antigo vira apelido do mesmo agente ou host, e o agente ou host juntado fica
  arquivado com os apelidos, as sessões e as conversas no que ficou.
- Ainda não há telas, o alerta `host_no_data`, a troca de workspace de um emparelhamento, nem classificação de conversa.

- English summary: external agent and host routes stop answering 501: list, get, rename, archive/unarchive, always-on
  (host only), and merge two agents or two hosts. Changes are owner/admin only and each writes an activity event. The
  old name becomes an alias of the same entity, and the merged entity is archived with its aliases, sessions and
  conversations moved to the one that stays. No screens, no `host_no_data` alert, no repository-pairing change yet.
