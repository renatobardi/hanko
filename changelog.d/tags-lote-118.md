### Tags de persona: edição em lote na lista de Personas

- A lista de Personas ganha a seleção múltipla, com "marcar todas", e uma barra para aplicar ou tirar uma tag das personas marcadas. Ao aplicar, um nome novo cria a tag. O lote roda numa transação só e grava um evento por persona alterada; se uma persona não puder receber a tag (teto de 20 ou tag arquivada), nenhuma recebe (ADR-0087, fatia 2).

- English summary: the Personas list gets multiple selection, with "select all", and a bar to apply or remove a tag from the selected personas. When applying, a new name creates the tag. The batch runs in a single transaction and records one event per changed persona; if one persona cannot take the tag (the 20-tag cap or an archived tag), none does (ADR-0087, slice 2).
