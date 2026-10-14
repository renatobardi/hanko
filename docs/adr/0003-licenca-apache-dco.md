# ADR-0003 — Licença Apache-2.0 + DCO; `ee/` reservado
Data: 2026-10-13 · Status: aceita

## Contexto
Nicho-alvo: enterprise regulado on-prem (bancos). Políticas corporativas comuns proíbem AGPL. A referência de mercado é MIT; entrar mais restritivo sem marca é atrito puro.

## Decisão
Core em Apache-2.0. DCO (`git commit -s`), sem CLA. `ee/` reservado para features enterprise sob licença comercial (open-core, modelo GitLab). Core nunca precisa ser relicenciado.

## Consequências
Sem proteção contra hyperscaler clonar o core — risco irrelevante no v0.1. THIRD_PARTY_NOTICES gerado por SBOM.
