# SESSION

- Correções de M5 na branch `fix/m5` (migration 00007): rede de segurança do heartbeat só acorda por mudança de status/atribuição de ator não-persona ou comentário humano que nenhuma run cobre (`tasks.last_human_change_at`); escrita da própria persona e edição de título/descrição/prioridade não geram laço de runs.
- Auth: firebase em URL pública exige lista de admissão ou `HANKO_AUTH_OWNER_EMAIL` (recusa o boot); formulário "Criar conta de dono" só em loopback ou `HANKO_AUTH_WEB_BOOTSTRAP=true`; semáforo de argon2id (503 + Retry-After); `X-Forwarded-For` só de `HANKO_TRUSTED_PROXIES`; bucket por e-mail (20/15 min) com backoff; sessão com teto absoluto de 90 dias; sair da lista de admissão derruba sessão e API key; JWKS vencido vale por até +1 h.
- Orçamento: recusa só por reserva silencia a persona na rede de segurança por 10 min (`personas.budget_refused_at`); status explícito de humano zera `pause_reason`. `run_events.seq` vem de `runs.next_event_seq` (sem corrida).
- Desvios: a admissão também vale para API key; o silêncio de 10 min vale para todo o candidato do heartbeat (ramos a e b).
- Próximo: M6.
