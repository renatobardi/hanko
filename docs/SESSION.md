# ADR-0089 · sessão `estudo-issues`
Data: 2026-10-31

## Feito
- ADR-0089 "Assistente Conectar ferramenta a uma persona" (#45), status proposta: cinco decisões do Bardi (modelos de acesso como dados por empresa, admin edita, copiar e não vincular, marco único, header e OAuth) e três recomendações aceitas.
- Ajuste achado no código: a descoberta de tools exige a integração já gravada (`tools_catalog.go:198`), então o passo 2 grava a integração (fechada e sem vínculo) e só o perfil, as regras e os vínculos vão juntos no fim.

## Travado
- Nada.

## Próximo passo
- Bardi: ler e aceitar (ou ajustar) o ADR-0089; depois o contrato (migration, tipos de domínio e OpenAPI), com gate dele.
