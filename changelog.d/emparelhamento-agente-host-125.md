### Fonte externa: agente, host e emparelhamento com workspace (fatia 1 da v0.8, #125)

- A ingestão agora liga cada fato às entidades da empresa, na mesma transação do lote: o agente externo e o host externo
  nascem do primeiro fato com esse nome (com `activity_event`), e um nome que chega com outra caixa vira apelido do mesmo
  agente. A hora do último fato do host só sobe.
- O repositório do fato se emparelha com o workspace de mesmo slug; sem ele, o Hanko cria um workspace só de metadado,
  sem caminho em disco. Fato sem repositório vai para o workspace de sistema `no-repo`. Empresa arquivada recebe o bruto
  e nenhuma entidade nova.
- Ainda não há tela, renomear, arquivar ou juntar agente e host, caixa de entrada para emparelhamento a conferir, corrigir
  emparelhamento, classificação de conversa e a conversa em si.

- English summary: ingestion now derives the external agent and host from each fact, in the same batch transaction. Agent
  names differing only in case become aliases of one agent; a host's last-seen time only moves forward. A fact's repository
  pairs with the workspace of the same slug, or gets a metadata-only workspace; facts without a repository go to the system
  `no-repo` workspace. Archived companies get raw telemetry only. No UI, rename/archive/merge, inbox items, pairing
  correction, classification or conversations yet.
