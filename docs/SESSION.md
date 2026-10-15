# Estado da última sessão
Data: 2026-10-15 · Trilha v2c (Sonnet 5.5): testes de API/CLI e exportar/importar empresa

- Testes de integração da API: assinaturas, quatro custos, trava semanal, alertas, revisor, nome amigável (`internal/api/*_integration_test.go`) e da CLI contra servidor real.
- Bug achado e corrigido: `UpdatePersona` ignorava `reviewer_agent/model` quando `reviewer` vinha junto (agora 422).
- `hanko export` / `hanko import` (ADR-0022): JSON `hanko.company.v1` sem ids nem segredos, execuções opcionais, import cria empresa nova numa transação.
- Exportar exige admin; importar segue a regra de criar empresa (`users.role` owner/admin); persona recebe 403.
- Não coberto: alertas não têm endpoint REST; import não restaura memória (ai-memory), gates nem consumo de orçamento.
