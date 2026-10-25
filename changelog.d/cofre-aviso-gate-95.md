### Cofre: aviso de alcance na aprovação do gate de contratação

- Aprovar um gate `hire` devolve os avisos de alcance da persona criada (campo `warnings` do gate, ADR-0068 §2.5), como a contratação direta já fazia: quando o `adapter_config` da ficha referencia um `${secret:…}` que a persona não alcança, a resposta da aprovação (API e CLI `hanko gate approve`) traz o aviso, que nomeia a persona e o item e nunca o valor. Continua sendo aviso, não bloqueio: a concessão pode vir depois.

- English summary: approving a `hire` gate now returns the reach warnings of the created persona (gate `warnings` field, ADR-0068 §2.5), matching direct hire: when the spec's `adapter_config` references a `${secret:…}` the persona cannot reach, the approval response (API and `hanko gate approve` CLI) carries the warning, naming the persona and the item, never the value. Still a warning, not a block: the grant can come later.
