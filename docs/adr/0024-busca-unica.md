# ADR-0024 — Busca única da empresa
Data: 2026-10-15 · Status: aceita

## Contexto
A tela "Buscar" era um "Em breve". Hoje só há um filtro `ILIKE '%…%'` no título/descrição das tasks (~75 ms com 30 mil
tasks, sem acento, sem ranking, sem trecho). Quem opera um time de personas precisa achar, num lugar só, a task, o
comentário onde algo foi decidido, a persona que cuida do assunto, a execução que fez e o fluxo que repete — e, quando
há memória, o que ela guardou. As próprias personas precisam do mesmo para não repetir trabalho.

## Decisão
1. **Full-text do Postgres, sem extensão.** Migration `00015_busca.sql`: colunas `search_tsv tsvector GENERATED ALWAYS … STORED`
   (config `simple`: sem stemming nem stopwords, que dependem de idioma e mutilam nomes e chaves) + índices GIN em
   `tasks` (título A, descrição B), `task_comments` (corpo), `personas` (nome/slug A, cargo B, instruções C), `runs`
   (nome amigável A, resumo B) e `flows` (nome/slug A, descrição B, modelo da task C). O texto entra cortado em 100 000
   caracteres, para um texto gigante nunca estourar o teto de 1 MB do `tsvector` num `INSERT`.
2. **Acento e caixa: função própria, sem `unaccent`.** `unaccent()` do contrib exige `CREATE EXTENSION` (privilégio
   que a migration de uma instalação qualquer pode não ter; o ADR/PERF já evitou o `pg_trgm` por isso) e não é
   `IMMUTABLE`, logo não entra em coluna gerada sem um wrapper. A migration cria `hanko_fold(text)` (`IMMUTABLE`, SQL puro):
   `translate()` dos acentos latinos comuns + `lower()`, e **pontuação ASCII vira espaço** (`src/a.go`, `a@b.com`,
   `HNK-12` e `foo_bar` viram palavras simples dos dois lados, em vez dos tokens compostos do parser). Não depende do
   locale do banco. A mesma tabela existe em Go (`internal/search.Fold`) e `TestFoldIgualAoSQL` as compara. Alfabetos
   fora do Latin não são dobrados (só minúsculos pelo `lower` do banco).
3. **Consulta.** `internal/search.Parse` monta a `tsquery` no app (nunca texto do usuário cru: palavras entre aspas
   simples, operadores gerados pelo código): palavras separadas = E; `"frase exata"` = adjacência (`<->`); `-termo` =
   exclusão; `OR` (maiúsculo) = alternativa; uma palavra com pontuação (`foo-bar`) vira frase; **a última palavra
   positiva casa por prefixo** (`:*`). No máximo 12 palavras de 64 caracteres. Sem nenhum termo positivo é `ErrInvalid`
   (422). A **chave exata** de uma task (`HNK-123`, com o prefixo da empresa) abre o grupo de tasks com essa task primeiro
   (`exact`); o resto da busca segue (menções da chave em comentários continuam acháveis).
4. **Ranking.** `ts_rank(search_tsv, q, 1) × (0,5 + 0,5 / (1 + idade_em_dias/30))` — relevância com os pesos A/B/C,
   normalizada pelo tamanho do documento, e um fator de recência de 1,0 (agora), 0,75 aos 30 dias e 0,5 (muito antigo);
   desempate por data e id. O relógio é o do service (`Now`), então é testável. **Tasks, comentários e runs ranqueiam
   só as 2000 candidatas mais recentes que casam** (CTE `cand` → `top` → joins): ranquear as 60 mil linhas de um termo
   onipresente custava ~0,5 s, e uma consulta que casa mais de 2000 documentos é genérica demais para um ranking global.
   Vale o mesmo limite nas três; personas e flows (poucas linhas) ranqueiam tudo. A `tsquery` entra como parâmetro
   direto (não numa CTE) para o planner enxergar a frequência do termo. Comentários ganham o índice
   `task_comments (company_id, created_at DESC, id)` para o caminho "termo comum".
5. **Trecho com marcas, sem HTML.** Em vez de `ts_headline` (que acha palavras no texto original, onde "ação" não casa
   com a consulta dobrada "acao", e devolve a marcação como texto), o trecho é montado em Go sobre as primeiras 20 000
   runas do texto, com a mesma dobra: janela de ~180 runas em torno da primeira palavra que casou e segmentos
   `{text, match}`. **O texto nunca é HTML**: a API devolve segmentos; a UI os escreve com escape do templ e põe `<mark>`
   só em volta dos que casaram; a CLI e o MCP imprimem texto (`«»` no MCP).
6. **Autorização (ADR-0021).** `Service.Search` exige leitura da empresa (viewer ou mais); não-membro recebe
   `ErrNotFound`. A matriz do ADR-0021 deixa todos os papéis lerem tudo da empresa (tasks, comentários, personas com
   instruções, execuções, fluxos), então não há filtro por papel dentro da busca. Toda consulta filtra por
   `company_id` (multi-org estrito); o dono da instalação busca em qualquer empresa. Run token = a empresa do token
   (como o resto da API e do MCP).
7. **Memória.** `memory.Provider` ganha `Search(ctx, scope, query, limit)`; `Noop` devolve vazio e o grupo "Memória"
   some. O escopo é só o workspace (slug da empresa): a busca vale para todos os projetos da empresa. A implementação
   `aimemory` chama a tool `memory_search` (`aimemory.SearchTool`) com `workspace`, `query` e `limit`. **O contrato
   dessa tool não está confirmado contra um servidor real** (nome, argumentos e formato do resultado vieram por
   suposição: não há outro uso no repositório); por isso o parser aceita JSON (lista ou `{results|hits|pages|items}`
   com `path/title/snippet`) e texto livre, e os testes usam um servidor MCP falso. Se o nome real for outro, troca-se a
   constante. A memória é dependência degradável (ADR-0015): erro ou mais de 3 s marcam o grupo como indisponível
   (`unavailable`) e o resto da busca vale.
8. **Superfícies.** Service `Search(ctx, companyID, q, SearchFilter{Types, Limit})` → grupos por tipo (`task, comment,
   persona, run, flow, memory`), `limit` por tipo (padrão 10, máx. 50; `more` quando passa do limite). API
   `GET /api/v1/companies/{company}/search?q=&types=&limit=`. CLI `hanko search <termo…> [--type …] [--limit n] [--json]`.
   MCP `hanko_search` (empresa do token; run token de persona permitido). UI `/c/{company}/buscar`: campo com htmx
   (`hx-get`, atraso de 250 ms, `hx-push-url`), troca só `#search-results`, filtro por tipo, estados vazio / sem
   resultado / indisponível; a tecla `/` (em `hanko.js`, ~15 linhas) foca o campo, ou abre a tela de qualquer outra. O
   item do menu sai de "Em breve".
9. **Export/import (ADR-0022).** As colunas geradas não entram no arquivo (o export lê campos nomeados) nem no `INSERT`
   da importação (o Postgres as calcula); coberto pelos testes de export/import existentes, que rodam sobre o schema novo.

## Consequências
- Busca acentuada, ranqueada e com trecho em todas as telas de dados, sem dependência nova. Termos seletivos custam 0,1–3 ms
  e tasks com termo onipresente ~20 ms; só termos em ~60 % de todas as linhas de comentários/runs passam de 50 ms
  (62–313 ms, porque o prefixo da última palavra não tem estimativa de frequência). Tabelas em `docs/PERF.md`.
- **Custo de escrita e de espaço:** uma coluna `tsvector` e um GIN por tabela. O custo vem em `INSERT`/`UPDATE` das colunas
  de texto (`tasks.title/description`, `runs.name/summary`...). `UPDATE runs SET next_event_seq = …` (o mais quente) não
  mexe nas colunas de origem, então a coluna gerada não é recalculada e o `UPDATE` continua HOT.
- **Custo de leitura:** os `SELECT *` do sqlc passam a trazer `search_tsv` (o modelo `store.Task` etc. ganha o campo
  `SearchTsv`, não usado). Medido em `docs/PERF.md`. Quem listar muitas linhas e quiser evitar o tráfego deve nomear as colunas.
- Aplicar a migration reescreve `tasks`, `task_comments`, `personas`, `runs` e `flows` (`ADD COLUMN … STORED`) e
  constrói os GIN, com bloqueio de escrita; em base grande, planeje a janela (~25 s com 80 mil runs, 60 mil comentários e 30 mil tasks; `docs/PERF.md`).
- O GIN não inclui `company_id` (sem `btree_gin`, que é extensão): termos muito comuns varrem os mesmos índices para
  todas as empresas e filtram depois. Aceito enquanto o índice for pequeno; o remédio, se doer, é particionar por empresa.
- Sem stemming: "implantar" não acha "implantação"; a busca por prefixo da última palavra cobre o caso comum de digitação.
- A busca da lista de tasks (`?q=` com `ILIKE`) continua como está; unificá-la com esta é trabalho futuro.
