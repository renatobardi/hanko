# Estado da última sessão (UI alinhada ao Kubo DS)
Data: 2026-10-15 · ramo `ui/aderencia-kubo-ds` · prd em `d2b1b51` (só fonte + tokens; migrations até 00025)

## Feito
- Fonte Inter local, tokens OKLCH do DS, cards com anel, escalas de raio/tipografia, botões/inputs/foco do DS, shell 256/72 (CSS).
- Glifo por persona (migration 00026, API, export/import, CLI `--icon`, seletor e avatares), quadro de tasks (`?visao=quadro`), tema claro/escuro/sistema (`POST /tema`), Configurações só no rodapé, estilos inline viram classes. Testes em `internal/ui/ds_test.go`.
## Travado
- Código gerado não regenerado na sessão (sem Go 1.27/sqlc/templ no ambiente): rodar `sqlc generate`, `templ generate -path internal/ui/views`, `gofmt -w .`, `go test ./...`, `make openapi` e commitar o gerado.
## Próximo passo
- Deploy do ramo no prd (`deploy-prd.sh`) e conferência visual contra o canvas; organograma (ilha React) ainda mostra iniciais em vez do glifo da persona.
