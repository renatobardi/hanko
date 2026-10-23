### Correção — integração MCP nova nasce fechada para escrita (ADR-0067)
- O formulário de servidor MCP novo não vem mais com "Vale para todas as personas" marcado e avisa que persona sem perfil recebe todas as tools.
- Integração criada a partir de agora: a primeira descoberta do catálogo põe em quarentena as tools que escrevem, apagam ou não declaram o risco (inclusive quando a descoberta acontece na primeira execução). Integrações já existentes mantêm a linha de base.
- Regra por risco do perfil exige escolha explícita; escolha vazia ou desconhecida é recusada em vez de virar "Permitido".
- English summary: new MCP integrations no longer default to all personas; their first catalog discovery quarantines write/destructive/unknown tools (existing integrations keep the baseline); profile risk rules require an explicit choice.
