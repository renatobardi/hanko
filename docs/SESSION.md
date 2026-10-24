# Estado da última sessão (cofre para agentes, fatia 2)
Data: 2026-10-24 · branch `sessao/57-reauth-passkey` · issue #57 (fatia 2 de #54, ADR-0070)

## Feito
- Reautenticação recente (`RequireReauth`, janela `reauth.minutes`, padrão 5) com provas por senha, Firebase (`auth_time`) e passkey; `/me/reauth*` e `/me/passkeys*` saíram do 501; passkey na tela de perfil com o diálogo de prova.
- `internal/auth/passkey`: contador de assinatura conferido (clone recusado); testes com autenticador de software.
## Travado
- Ações do cofre (revelar, histórico, restaurar, envio, concessão) seguem 501: entram com as fatias 1, 3 e 4, e chamam `RequireReauth`.
- Decisões para o Bardi: a chave é `reauth.minutes` (o ADR-0070 diz `secrets.reauth_minutes`, mas a guarda do registro, ADR-0032, recusa nome com "secret"); e o fato vai para cada empresa do usuário (`activity_events.company_id` é obrigatório).
## Próximo passo
- Fatia 3 (#58): ligar `RequireReauth` em revelar e histórico. Prova por Firebase pelo SDK na tela ainda não existe (o diálogo só avisa).
