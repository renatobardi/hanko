# Desligar o agent-studio no Hanko (#66) · sessão `hanko-decom-agent-studio`
Data: 2026-11-01

## Feito
- Fatia 2 (#126) e fatia 3 (#127) em código na `main`: custo de lista, histórico de mensalidade e pago único, conferência de preços, 17 alertas externos mais `db_disk_low`, "vence em" na Caixa; rodada, ciclo, etapa, ações e marca, aprovação externa com script restrito, itens da Caixa. Da fatia 4 (#128): export e import da configuração externa (#195).
- v0.8.3 cortada (#196) e compose com `pgdata` somente leitura (#197); tag e deploy são do Bardi. Issues #66, #125 a #128 atualizadas.

## Travado
- Quem liga a trava de preço (#126); dia de negócio do rateio (#191); #81 antes do ensaio. Demos `qa` das fatias 1 a 4 precisam de coletor real e de telas.

## Próximo passo
- Fatia 4: busca dos conceitos externos e telas (templ) da fonte externa, rodada e aprovação; depois #129 (Hanko Desktop).
