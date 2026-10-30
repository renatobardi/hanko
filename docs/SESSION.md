# Contrato das tags de persona · sessão `118-tags-contrato`
Data: 2026-10-29

## Feito
- Migration 00212 (cadastro `persona_tags`, ligação `persona_tag_links` com chaves por empresa, `personas.tag_names` com peso B na busca), testada sobre a 00211 e no Down.
- Domínio (`internal/domain/persona_tags.go`): paleta de 8 cores, limites, chave sem caixa, verbos dos eventos, any/all, highlight/prune, nível.
- 11 operações em 501 (`internal/api/persona_tags.go`); nomes dos comandos da CLI no comentário do contrato.

## Travado
- Gate do Bardi no contrato (#118): paleta, `untagged` por OU e `POST /personas/{persona}/tags` são recomendação do autor.

## Próximo passo
- Com o ok do Bardi: fatia 1 (cadastro, aplicação, filtros, API, CLI, MCP, busca, export), depois fatia 2 (lote).
