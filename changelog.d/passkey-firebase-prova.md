### Perfil: prova recente pelo Google para contas do Firebase (#98)

- Em **Meu perfil › Passkeys**, a conta do provider `firebase` confirma o diálogo "Confirme sua identidade" pelo Google (login do SDK, ID token com `auth_time` recente) e a ação que abriu o diálogo é repetida. Antes, a conta só via "Entre de novo para confirmar" e não cadastrava a primeira passkey (ADR-0070 §3).
- Conta `local` e conta com passkey seguem como antes.
- English summary: on the profile page, Firebase accounts confirm the "Confirm your identity" dialog with a Google sign-in through the Firebase SDK (ID token with recent `auth_time`), and the action that opened the dialog is repeated. Before, these accounts could not register their first passkey (ADR-0070 §3).
