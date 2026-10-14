# ADR-0004 — Adapter `cli` primeiro; ACP em v0.2
Data: 2026-10-13 · Status: aceita

## Contexto
ACP (Agent Client Protocol) é o alvo certo — é o que a referência de mercado usa como engine default, mantendo CLI como fallback. Mas custa 1–2 semanas de protocolo (sessão persistente, quota, auth, fallback) antes de ver um agente rodar.

## Decisão
v0.1: interface `Adapter` + engines `cli` (`claude -p --output-format stream-json --verbose`; `codex exec --json`) e `http` (webhook). `acp` (JSON-RPC 2.0 via stdio, client próprio em `internal/acp`) em v0.2.

## Consequências
Sem sessão persistente nem permissões interativas no v0.1. Troca de engine não toca domínio. Agentes ACP são npm; "zero Node" vale só pro binário.
