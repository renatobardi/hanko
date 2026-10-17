# Estado da última sessão (plano de evolução v0.3 → v0.6)
Data: 2026-10-16 · branch `docs/plano-evolucao-v03-v06` · só documentação

## Feito
- Assessment de lacunas por domínio (funcional, técnico, processos, operação) e decisão item a item pelo dono: 70 itens, 8 fora.
- `docs/ROADMAP.md` reescrito: trilha contínua de qualidade/operação + 4 ondas (Fundações, Execução, Governança, Ecossistema), M7–M20, ADRs 0032–0048 previstos, lista do que ficou fora.
## Travado
- Nada. Provider de sandbox cloud (M11) e de cofre externo (M7) se escolhem no ADR de cada marco.
## Próximo passo
- Trilha contínua "Já": publicar `v0.1.0`/`v0.2.0`, `SECURITY.md` + dependabot, separar CI, metas de cobertura.
- M7: ADR-0032 (segredos) e ADR-0033 (BlobStore), depois o contrato (migrations + OpenAPI).
