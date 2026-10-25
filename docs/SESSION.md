# Diálogo de prova recente com Firebase · issue #98
Data: 2026-10-25 · sessão `98-prova-firebase` (rodada swarm-1024-0625, fase `build`)

## Feito
- Diálogo "Confirme sua identidade" (perfil › Passkeys) oferece "Confirmar com Google" quando a API lista `firebase`: login do SDK, ID token novo, `POST /me/reauth` com `method: firebase`, e a ação que abriu o diálogo é repetida.
- Backend sem mudança: `ReauthWithFirebase` já confere uid e `auth_time` (teste `TestProvaFirebaseExigeUidEAuthTimeRecente`).
- Testes: UI (página do perfil nos dois providers, JS das provas e do login do Google com SDK falso) passam localmente.

## Travado
- Testes de integração de `internal/service` (reauth) não rodaram aqui: sem `HANKO_TEST_DATABASE_URL`. Vão no CI.

## Próximo passo
- Revisar o PR; demo do critério `qa` com a conta Google do Bardi (cadastrar a primeira passkey; 5 min depois, pedido de prova de novo).
