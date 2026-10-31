# Roteiro da demo da ingestão externa · sessão `estudo-issues`
Data: 2026-10-31

## Feito
- `docs/DEMOS.md` e `docs/en/DEMOS.md`: roteiro da demo `qa` da #125 num Hanko local (listener `HANKO_INGEST_ADDR`, chave `ingest`, fato OTLP em JSON pelo replay, leitura pela API).
- Achados lendo o código: `HANKO_INGEST_ADDR` não aceita `0.0.0.0` nem o mesmo endereço de `HANKO_ADDR` (`internal/config/config.go:537-545`); a API de chaves `ingest` já está ligada (`internal/api/ingest_keys.go`).

## Travado
- O roteiro não foi executado: este ambiente não tem Go nem Postgres. O Bardi roda e anota o passo que falhar.

## Próximo passo
- Bardi: rodar o roteiro num Hanko local; se passar, marcar o `qa` na #125 (e conferir o que falta nas fatias 3 e 4 antes de fechar).
