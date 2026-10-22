# Estado da última sessão (correção dos compositores de chat)
Data: 2026-10-22 · branch `fix/compositor-chat`

## Feito
- Causa do texto/anexo sumindo: o autosave do rascunho disparava a limpeza do form (hx-on::after-request). Limpeza movida para hanko.js, só no envio 2xx do form[data-composer]; autosave com `hx-sync="closest form:abort"`.
- Envio que "não aparecia": poll da conversa suspenso/cancelado durante o envio; thread da task com polling (`/tasks/{task}/conversa`); erros 5xx/rede no #flash; botão desabilitado no envio.
- Testes: unitários em `internal/ui/composer_test.go`, e2e `60-chat.spec.ts` (falha sem a correção, passa com ela).
## Travado (só o dono)
- Itens de `docs/VALIDACAO-MANHA.md` continuam (tags, deploy no hanko-prd).
## Próximo passo
- Deploy da main com a correção e validar digitando no chat em produção.
