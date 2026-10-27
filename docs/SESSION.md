# Cofre pela interface · sessão `117-cofre-tela-cli`
Data: 2026-10-27

## Feito
- Item 1 da #117: "Novo campo" aceita o tipo `file` (envio de até 1 MiB), sempre secreto; o conteúdo nunca volta à página. "Apagar campo" para trocar o arquivo (ADR-0068 §5.1). Testes em `internal/ui/cofre_campo_arquivo_test.go`.

## Travado
- Nada. A ordem dos itens 2 (delegação, tela e CLI) e 3 (env da persona) é decisão do Bardi.

## Próximo passo
- Bardi decide a ordem dos itens 2 e 3 (pergunta no comentário da #117).
