# ADR-0071 — Banco de teste próprio por teste, clonado de um template migrado
Data: 2026-10-24 · Status: aceita · Issue: #71

## Contexto
O `internal/testdb.New` recriava o schema `public` do banco de `HANKO_TEST_DATABASE_URL` e aplicava todas as migrations
a cada chamada. Com 70 arquivos de migration (148 tabelas), cada chamada custava ~1,1 s sem `-race` e ~2 s com `-race`
(medido localmente, Postgres 16). O `internal/service` chama o helper 333 vezes (`newEnv`/`newEnvEnv`), então passava
~17,6 min no CI só com o pacote, e era o caminho crítico do `check` depois da #51.

## Decisão
1. **As migrations rodam uma vez por processo de teste**, num banco template `<base>_tpl_<hash>`, onde `<base>` é o banco
   da URL e `<hash>` resume nome e conteúdo de cada migration. Migration nova ou alterada gera template novo.
2. **Cada `testdb.New` cria um banco próprio** (`CREATE DATABASE <base>_t<pid>_<n> TEMPLATE <template>`) e o apaga no
   `t.Cleanup` (`DROP DATABASE ... WITH (FORCE)`). Medido: ~55 ms por teste com `-race`, criação e remoção juntas.
3. **O banco da URL só serve de conexão administrativa** (criar e apagar os outros). Testes que usam a URL direto, como
   os de migration, o de restore, o `pgchaos` e os do `heartbeat`, continuam no banco da URL, sem mudança.
4. **Concorrência:** um advisory lock no servidor serializa a criação do template entre processos. O template é montado
   com nome temporário (`<template>_build`) e renomeado só quando completo; um processo morto no meio não deixa template
   pela metade.
5. **A assinatura não muda:** `testdb.New(t) *pgxpool.Pool`. Nenhum teste muda o que afirma.

## Consequências
- `internal/service` cai de 1036 s para 130 s localmente, com os mesmos 654 testes e a mesma cobertura (77,9 %).
- O usuário da URL precisa poder criar banco (`CREATEDB`). O Postgres do CI e o de dev usam superusuário.
- Sobra um template por versão das migrations no servidor de teste. Um banco por teste sobra só se o processo morrer
  antes do `t.Cleanup` (ex.: timeout do `go test`). Ambos são descartáveis (`DROP DATABASE`), e no CI o Postgres é
  recriado a cada job.
- `-p 1` continua: os testes que usam a URL direto ainda resetam o mesmo banco. `t.Parallel()` no `service` fica fora:
  com o pacote em ~2 min, ele deixou de ser o caminho crítico do `check`.

## Alternativas descartadas
- `TRUNCATE` das tabelas entre testes: apagaria os dados que as migrations semeiam e exigiria manter a lista de tabelas.
- Uma transação revertida por teste: o service abre as próprias transações e há testes com várias conexões (corrida,
  locks), que não enxergariam os dados uns dos outros.
- Dividir o `service` em mais shards por `-run`: só divide o mesmo custo entre mais runners e complica a conferência da
  união dos shards e a cobertura do pacote.
