# ADR-0001 — Implementação do zero em Go
Data: 2026-10-13 · Status: aceita

## Contexto
A categoria "control plane de agentes" já tem um líder open-source com ~1,3M LOC TS, 68 tabelas, 342 migrations, 12 adapters e time full-time. Paridade é inviável solo.

## Decisão
Hanko implementa os **conceitos** da categoria (company, agent, issue, heartbeat, run, approval, budget, routine) do zero, em Go, single binary. **Nenhum código de terceiros.** Produtos de terceiros não são citados em artefatos do projeto (docs, código, UI, site).

## Consequências
Sem compatibilidade com outros produtos. Sem obrigação de atribuição (nada copiado). Escopo governado por milestones (ADR-0002).
