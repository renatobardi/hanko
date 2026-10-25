# ADR-0074 — Marca explícita de senha em campo
Data: 2026-10-25 · Status: aceita · Issue: #61 (parte de #54) · Completa: ADR-0068 §7.2
Aceite: 2026-10-25, aceito pelo Bardi.

## Contexto
O ADR-0068 §7.2 mede o achado "fraco" (entropia abaixo de 60 bits) só em campo marcado como senha. Hoje a marca vale
pelo nome do campo (`password` ou `senha`). Um campo `chave` ou `pin` com valor fraco fica de fora do relatório. O §7.2
diz "marca explícita", mas não define a forma. O PR #79 deixou isso em aberto.

## Decisão
1. **Forma: coluna booleana.** `secret_fields.password boolean NOT NULL DEFAULT false` (migration 00207). Não é um valor
   novo de `kind`: `kind` diz como o valor é guardado e revelado (ADR-0068 §1.2), e a marca não muda isso. Um valor novo
   de `kind` mexeria nos enums da API, dos envios e do import.
2. **Só texto secreto.** A marca vale para `kind = text` com `sensitive = true`. A regra fica no banco (CHECK) e no
   domínio (`domain.ValidatePasswordMark`).
3. **Quem marca.** `POST …/fields` (campo `password`), `PATCH …/fields/{field}` (campo `password`) e a CLI
   (`hanko secret add --password`; `hanko secret set --password` ou `--no-password`). Vale a mesma permissão de mudar a
   marca secreto/visível de um campo.
4. **Relatório.** Entra no achado "fraco" o campo marcado **ou** o campo com nome `password`/`senha`. A marca soma com a
   regra do nome (§7.2 continua valendo). O limite de 60 bits não muda, e o valor só é decifrado para esses campos.
5. **Fora desta fatia.** Export e import (ADR-0068 §8) não levam a marca. Campo que já existe nasce com `password = false`
   e não ganha marca sozinho. A tela de cofre não tem o controle de marca.

## Alternativas descartadas
- **Valor novo de `kind` (`password`).** Mudaria o tipo do campo, que a API, os envios e o import leem. A marca é um
  atributo de leitura do relatório, não um tipo.
- **Tabela à parte com as marcas.** Exigiria um join a mais no relatório e uma escrita a mais por campo, sem ganho.

## Consequências
- Campo marcado entra no relatório de fracos mesmo com outro nome. Campo sem marca e sem nome de senha continua fora.
- Um `.hkv` importado recria o campo sem marca até uma fatia que leve a marca no export e no import.
- Nenhuma chamada de rede. O relatório continua só para admin.
