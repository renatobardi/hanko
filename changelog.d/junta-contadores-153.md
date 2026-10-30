### Fonte externa: junção soma o agregado diário e escolhe a lista da outbox (#153)

- Juntar agente ou host soma os contadores do agregado diário (`calls_daily`) do juntado nas linhas do que fica com a
  mesma chave (dia, host, workspace, modelo, assinatura). Linha sem colisão só troca de dono. Repetir a junção não soma de novo.
- Juntar host escolhe a lista da outbox (`external_outbox_snapshot`) de hora mais nova; em empate fica a do que fica.
- English summary: merging an external agent or host sums the juntado's daily aggregate (`calls_daily`) into the
  surviving rows with the same key; rows without collision just change owner. Merging a host keeps the newest outbox
  snapshot, and on a tie the surviving host keeps its own. Repeating a merge does not sum again.
