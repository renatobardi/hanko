### Cofre para agentes: reautenticação recente e passkey (ADR-0070)

- Ação sensível pede prova recente de identidade: senha (provider `local`), token do Firebase com `auth_time` recente (provider `firebase`) ou passkey. A janela padrão é de 5 minutos e pode ser mudada com `HANKO_REAUTH_MINUTES` (1 a 60). Sem prova, a API responde 401 com `reauth_required`, e a tela abre o pedido de prova.
- Passkey por usuário na tela de perfil (até 10 por usuário), com verificação de usuário obrigatória e contador de assinatura conferido. Cadastrar e remover passkey também exigem prova recente.
- API key de escopo `read` recebe 403 em ação sensível; a de escopo `full` passa, e o fato `auth.*` registra o id da chave.
- Fatos `auth.reauthenticated`, `auth.passkey_added` e `auth.passkey_removed` vão para cada empresa ativa do usuário.
- English summary: sensitive actions now require a recent identity proof (password, Firebase `auth_time`, or passkey) for 5 minutes by default (`HANKO_REAUTH_MINUTES`); the profile page manages passkeys; read-scope API keys get 403 and full-scope keys pass with their key id on the audit fact.
