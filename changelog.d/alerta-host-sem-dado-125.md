### Fonte externa: alerta host_no_data (#125)

- Host externo sempre ligado, não arquivado, sem fato há mais do limite da empresa abre o alerta `host_no_data` (critical). O limite é `limit.host_no_data_minutes` (padrão 30 min). Quando o fato chega, o alerta fecha sozinho (ADR-0077, item 4; ADR-0083, itens 2 e 5).
- English summary: an always-on external host without facts for longer than the company's limit opens a critical `host_no_data` alert; the next fact closes it.
