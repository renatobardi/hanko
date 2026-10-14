# ADR-0018 — Endurecimento do modo authenticated (correções de M5)
Data: 2026-10-14 · Status: aceita

## Contexto
A revisão independente do M5 achou que o primeiro visitante de uma instalação pública virava dono, que o argon2id podia ser usado para esgotar memória/CPU,
que `X-Forwarded-For` de qualquer par privado era aceito, que o limite de login não cobria força bruta distribuída e que a sessão e o JWKS não tinham teto.

## Decisão
1. **Dono**: provider `firebase` com `HANKO_PUBLIC_URL` fora de loopback exige `HANKO_AUTH_ALLOWED_EMAILS/DOMAINS` ou `HANKO_AUTH_OWNER_EMAIL` (recusa o boot).
   Com dono nomeado, só esse e-mail vira `owner`. Provider `local`: o formulário web de criação do dono só em loopback ou com `HANKO_AUTH_WEB_BOOTSTRAP=true`.
2. **Custo**: semáforo de `max(2, NumCPU)` vagas em volta de todo hash/verificação argon2id; espera máxima de 2 s, depois 503 com `Retry-After`.
3. **IP do cliente**: `X-Forwarded-For` só de pares em `HANKO_TRUSTED_PROXIES` (padrão loopback); cadeia lida da direita para a esquerda.
4. **Força bruta**: bucket por e-mail (20 falhas/15 min, todos os IPs) com backoff progressivo, além de par IP+e-mail e IP.
5. **Sessão**: teto absoluto de 90 dias (`sessions.absolute_expires_at`); no firebase com listas, sessão e API key de quem saiu da lista deixam de valer.
6. **JWKS**: refresh falho usa o cache vencido por até 1 h (aviso no log); depois falha fechado.

## Consequências
Atrás de proxy fora do loopback (Caddy do compose) é preciso listar a sub-rede em `HANKO_TRUSTED_PROXIES`, senão o rate limit enxerga o IP do proxy.
O estado do limitador e do semáforo é por processo (v0.1 tem uma instância).
