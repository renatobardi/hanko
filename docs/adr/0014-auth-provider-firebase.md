# ADR-0014 — Provider de auth `firebase` (Google + email/senha)
Data: 2026-10-13 · Status: aceita

## Contexto
hanko.oute.pro precisa de login Google e email/senha sem manter base de usuários própria; o autor já opera um projeto Firebase para outros produtos. Self-host enterprise, porém, não pode depender de SaaS do Google para autenticar.

## Decisão
Provider `firebase` em `HANKO_AUTH_PROVIDER` (+ `HANKO_FIREBASE_PROJECT_ID`). Fluxo: host carrega Firebase Auth SDK (bundlado por `tools/bundle`), usuário autentica (Google ou email/senha), browser envia ID token para `POST /auth/firebase`, servidor valida assinatura via JWKS público do Google, `iss`/`aud` do projeto, e cria sessão cookie própria. Sem Firebase Admin SDK. Provider `local` continua default do self-host.

Rejeitado: (B) só firebase — quebra a tese on-prem; (C) OIDC genérico agora — v1.

## Consequências
Usuário é identificado por `uid` do Firebase mapeado em `users.external_id`. Mesma tabela `users` para ambos os providers. Revogação = expirar sessão própria.
