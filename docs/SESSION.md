# Telas do cofre com prova por Google e passkey · issue #92
Data: 2026-10-25 · sessão `92-hkv-reauth-firebase-passkey` (rodada swarm-1024-0625, fase `build`)

## Feito
- Export/import do `.hkv` e import de `.env`: quando o servidor pede prova, a tela traz o botão "Confirmar identidade" (`ReauthHost`), que abre o diálogo do `passkey.js` (`ReauthDialog`, extraído da seção do perfil). A prova grava na sessão por `POST /me/reauth`; o formulário seguinte vai sem senha da conta. Senha no formulário continua valendo.
- Backend sem mudança. Testes: service por prova (senha, Google, passkey), UI das três telas por método, JS do diálogo nas telas do cofre.

## Travado
- Nada. A prova por Google no JS das telas do cofre usa o código do perfil (#98); o teste node cobre senha, passkey e o SDK que não carrega.

## Próximo passo
- Revisar o PR; demo com conta Google e com conta só com passkey (exportar o `.hkv` depois da prova).
