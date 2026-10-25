# Correção do nome da chave da janela no ADR-0070 · issue #57
Data: 2026-10-25 · sessão `57-adr-0070-nome-da-chave` (rodada swarm-1024-0625, fase `build`)

## Feito
- ADR-0070 §2: chave corrigida para `reauth.minutes` (variável `HANKO_REAUTH_MINUTES`, padrão 5, 1 a 60) e nota datada de 2026-10-25 com o motivo: a guarda do registro (ADR-0050 §C, `SensitiveName`) recusa `secret`; a isenção vale só para as duas chaves de histórico (ADR-0068 §3.2). A citação da guarda é o ADR-0050 §C, não o ADR-0032 (que é "Cards de status").
- Sem mudança em `internal/`; CHANGELOG já documenta `HANKO_REAUTH_MINUTES`. PR aberto com `Refs #57`.

## Travado
- Nada.

## Próximo passo
- Dispatcher/Bardi: revisar e fazer merge do PR; o critério `qa` (demo) da issue #57 segue em aberto.
