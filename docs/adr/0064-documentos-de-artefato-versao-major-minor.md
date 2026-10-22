# ADR-0064 — Documento de artefato com identidade estável e versão MAJOR.MINOR
Data: 2026-10-22 · Status: aceita · Altera: ADR-0028 (A.1, A.5, A.6 e adendo "Vínculo com a task"), ADR-0052 (anexos)

## Contexto
O dono relatou dois defeitos no uso real dos artefatos:

1. **Alteração vira documento novo.** Ao pedir uma mudança grande ou pequena num relatório ou plano, a persona publica
   outro arquivo (outro nome, outra "taxonomia") em vez de uma versão nova do mesmo. Causa: a identidade do documento é a
   cadeia (empresa, task, nome) do ADR-0028. Basta outro nome (`plano-v2.md`), outra task, o chat ou uma run sem task para
   abrir uma cadeia nova.
2. **A persona não acessa o que publicou.** O ADR-0028 deixou de fora, de propósito, as tools MCP de leitura ("o agente já
   está no workspace"). Na prática, a run seguinte não tem o arquivo, o briefing não lista os artefatos e o dono teve de
   reenviar várias vezes, na conversa, o artefato que a própria persona tinha gerado. Sem enxergar o documento, a persona
   também não tem como saber o nome que deveria reaproveitar, e o defeito 1 decorre daí.

Além disso, a versão é um inteiro (`v1`, `v2`…) que não diz se a mudança foi profunda ou um ajuste. O dono quer o mesmo
padrão em todo o app: toda alteração de artefato é classificada como **major** (pesada) ou **minor** (mínima), todo
artefato tem versão, e as versões anteriores podem ser abertas, comparadas e restauradas.

As decisões abaixo foram tomadas pelo dono, pergunta a pergunta, em 2026-10-22.

## Decisão

### 1. Documento com identidade estável
- Nova entidade **documento de artefato** (`artifact_documents`): `id` (uuid) estável, empresa, classe
  (`artifact|attachment`), nome atual, **projeto**, **workspace** e **tags livres** (`text[]`, minúsculas, 1–40
  caracteres `[a-z0-9-]`, até 20 por documento). Cada linha de `artifacts` continua sendo **uma versão** e passa a
  apontar para o documento (`document_id`).
- **Para alterar, informa-se o documento** (`artifact` = id). O nome pode mudar entre versões: o nome do documento é o da
  versão mais nova.
- **Projeto, workspace e tags identificam e filtram, mas não fazem parte da identidade.** Mover um documento de projeto ou
  trocar tags não cria documento novo. Na criação, projeto e workspace são herdados (projeto da task, workspace da task ou
  do projeto); depois são editáveis.
- **Task e run ficam em cada versão** e registram onde ela foi produzida. Um documento pode ter versões de várias tasks,
  runs e conversas. Filtrar por task traz os documentos que têm **alguma** versão produzida naquela task.
- **Compatibilidade.** Sem `artifact`, a publicação procura um documento de **mesmo nome** no mesmo escopo de antes (a
  mesma task, ou, sem task, a mesma conversa ou o "sem task" da empresa). Se achar, cria a versão seguinte dele; se não
  achar, cria um documento novo. O que funciona hoje continua funcionando.
- Anexos (ADR-0052) também viram documentos, com uma única versão `1.0`. O comportamento deles não muda.

### 2. Versão MAJOR.MINOR
- Toda versão tem `version_major` e `version_minor`. A primeira é **1.0**. Uma major leva a `(M+1).0` e uma minor a
  `M.(m+1)`. O inteiro `version` continua como **ordinal** da publicação dentro do documento (1, 2, 3…), para ordenar e
  para compatibilidade. Há índice único em (documento, ordinal) e em (documento, major, minor).
- Publicar de novo o **mesmo conteúdo** (sha256 e MIME iguais aos da versão mais nova) continua sem criar versão.
- Não há PATCH. Correção de digitação é minor.
- **Migração:** cada cadeia existente vira um documento, e a versão `vN` vira **`N.0`**. Não dá para saber a natureza das
  mudanças antigas, então cada uma conta como major.

### 3. Quem classifica: a persona declara, o servidor confere
- A publicação aceita `change` = `major|minor` e `change_reason` (motivo curto, até 280 caracteres; obrigatório quando
  `change` é informado por persona).
- O servidor **mede** a mudança contra a versão mais nova, de forma pura e determinística, com
  `domain.MeasureChange`/`domain.ClassifyChange`:
  - texto (UTF-8 até 512 KiB por lado, mesmo limite do diff): `ratio = (linhas removidas + adicionadas) /
    (linhas antigas + linhas novas)`, com o diff por linha do `internal/textdiff`;
  - **sugere major** quando o MIME muda ou `ratio ≥ 0,40`; **sugere minor** quando `ratio < 0,10`; entre os dois, **não
    opina**;
  - binário ou acima do limite: não opina.
- Resultado:
  - **declarou e não há contradição:** vale o declarado;
  - **declarou e a medida contradiz** (declarou minor e a sugestão é major, ou o contrário): vale o declarado, a versão é
    gravada com `change_disputed = true` e a tela mostra "classificação questionada". Assim o humano vê a divergência,
    sem que o servidor sobreponha a intenção de quem escreveu;
  - **não declarou** (humano na UI ou CLI sem escolher): vale a sugestão; sem sugestão, minor. Na UI o formulário já vem
    marcado com a sugestão.
- A versão grava `change_kind` (final), `change_declared` (o que foi declarado, nulo se nada), `change_reason`,
  `change_ratio` (nulo se não medido) e `change_disputed`.
- **Reclassificar** (member+): só a **versão mais nova**, que é renumerada (ex.: `1.3` → `2.0`). Versões anteriores nunca
  são renumeradas, porque já podem ter sido citadas em gate, revisão ou comentário. Gera o fato `artifact.reclassified`.

### 4. Acesso a versões anteriores e restauração
- Toda versão continua acessível: prévia, download, diff entre quaisquer duas e histórico com `M.m`, autor, task/run de
  origem, classificação, motivo e alerta de disputa.
- **Restaurar** (member+; persona na regra do item 6) cria uma **versão nova por cima**, sempre **major**, com
  `restored_from` = a versão de origem e o motivo "restaurada de X.Y". Exemplo: na 3.1, restaurar a 1.2 gera a 4.0. O
  histórico é imutável. Gera o fato `artifact.restored`.

### 5. A persona enxerga e lê os documentos (muda o ADR-0028 A.6)
- **Briefing:** a seção "Artefatos" (runs de task, de chat e de sala) lista até 10 documentos: os que têm versão na task,
  depois os do projeto da task, depois os que a persona publicou por último (o caso do chat, que não tem task). Cada um vem
  com id, nome, versão atual e tags. O protocolo manda **alterar com `artifact=<id>`
  em vez de publicar com outro nome** e declarar `change`.
- **MCP:**
  - `hanko_artifact_list`: filtros projeto, workspace, task, tag e busca pelo nome; devolve só a versão mais nova de cada
    documento;
  - `hanko_artifact_get`: documento e versão (padrão: a mais nova; aceita `version="1.2"`). Texto vem em `content` e
    binário em `content_base64`, com o mesmo teto por arquivo;
  - `hanko_artifact_put` ganha `artifact`, `change`, `change_reason` e `tags`; com `artifact`, `name` pode faltar e vale o
    nome do documento (a taxonomia não muda por descuido); a resposta traz o id do documento, `M.m`, a classificação final
    e se ficou questionada;
  - `hanko_artifact_restore`: documento e versão.
- As tools de leitura são `ReadOnlyHint` e entram no perfil padrão. A política de tools (ADR-0053) pode restringi-las por
  persona.

### 6. Papéis (altera o ADR-0028 A.5)
- `viewer` lê. `member` publica, edita projeto/workspace/tags, restaura e reclassifica. `admin` exclui o documento
  (todas as versões, definitivo).
- **Persona (run token):** lê qualquer documento da empresa (a leitura equivale a `viewer`). Publica versão nova ou
  restaura documento cujo projeto é o mesmo da **task** dela (nunca o `project` informado na chamada), ou documento sem
  projeto. Pela busca por nome (sem id), um documento de outro projeto não é dela: a publicação cria outro documento em vez
  de falhar. A versão grava a task e a run do token. A persona define tags só ao criar um documento; reclassificar e
  editar tags de um documento existente cabe ao humano.

### 7. Apagar a task preserva o histórico (substitui o adendo "Vínculo com a task" do ADR-0028)
- Com o documento atravessando tasks, o `CASCADE` apagaria versões do meio do histórico. Um gatilho `BEFORE DELETE` em
  `tasks` desliga as versões de artefato (`kind = 'artifact'`): `task_id = NULL`, e marca o documento como `detached`. Um
  documento `detached` fica fora da busca por nome do escopo "sem task", para que uma publicação nova sem task não vire
  versão de um documento órfão. Anexos continuam sendo apagados com a
  task. O gatilho fica no banco para cobrir todos os caminhos de exclusão (task, projeto, empresa), não só o service.
  O problema que motivou o `CASCADE` (versões órfãs se fundindo com a cadeia "sem task") deixa de existir, porque a
  unicidade agora é por documento.

### 8. Fatos, interfaces e export
- Fatos na mesma transação: `artifact.created` (payload com documento, `M.m`, classificação e disputa),
  `artifact.restored`, `artifact.reclassified`, `artifact.document_updated` (projeto, workspace, tags) e
  `artifact.deleted`.
- API: as rotas por id de versão continuam (`…/artifacts/{id}`, `/content`, `/diff`) e ganham `POST …/artifacts/{id}/restore`
  e `POST …/artifacts/{id}/reclassify`. O documento tem `GET …/artifact-documents/{documento}/versions/{M.m|latest}` e
  `PATCH …/artifact-documents/{documento}` (projeto, workspace, tags). O envio (`POST …/artifacts`) aceita `document_id`,
  `change`, `change_reason` e `tags`; a listagem filtra por `workspace`, `tag` e `document`. As respostas trazem
  `document_id`, `semver`, `major`, `minor`, a classificação e as tags.
- CLI: `hanko artifact put --artifact ID --change major|minor --reason … --tag …`, `hanko artifact restore|reclassify|tag`,
  `get <documento> --version M.m` e `list --tag --workspace --artifact`.
- UI `/artefatos`: lista por documento com a versão atual `M.m` e filtros de projeto, workspace, task, tag, persona, tipo
  e nome. O detalhe mostra o histórico com selo major/minor, motivo e alerta de disputa, o diff entre duas versões
  escolhidas, Restaurar e Reclassificar (só na mais nova), e a edição de tags/projeto/workspace. O envio tem a escolha
  major/minor pré-marcada pela sugestão.
- Export `hanko.company.v1`: o campo `artifacts` ganha `document`, `major`, `minor`, `change`, `change_reason`, `tags`
  e `document_project` (opcionais). Arquivo antigo importa como hoje, agrupado por cadeia, com `N.0`. O workspace do
  documento não é exportado: na importação ele fica vazio. A cota e os limites por arquivo não
  mudam.

## Alternativas descartadas
- **Manter (task, nome) e só melhorar o briefing:** a alteração pedida em outra task ou no chat continuaria abrindo
  documento novo.
- **Identidade por (projeto, nome):** cobre tasks diferentes, mas não a troca de nome, que é o caso mais comum.
- **Classificação só pelo servidor (tamanho do diff):** não enxerga mudança de sentido em poucas linhas (uma premissa ou
  uma conclusão invertida).
- **Classificação só pela persona:** inconsistente entre modelos e sem nenhum sinal para o humano.
- **MAJOR.MINOR.PATCH:** um terceiro nível que nem o dono nem a persona distinguem com consistência.
- **Restaurar como minor ou só consultar:** voltar a uma versão antiga é, por definição, uma mudança pesada sobre a
  atual, e só consultar não resolve o caso de uso.

## Consequências
- A migration cria um documento por cadeia existente e renumera tudo para `N.0`. Links antigos por id de versão continuam
  válidos.
- A listagem deixa de calcular "a mais nova da cadeia" por subconsulta correlacionada e passa a agrupar por
  `document_id` (índice próprio).
- A medida do diff roda sob a trava de artefatos da empresa. No driver padrão (Postgres) é uma leitura na mesma
  transação, com teto de 512 KiB e o teto de células do diff. Com S3 inclui um GET dentro da trava, e as gravações de
  artefatos da empresa esperam por ele. Medir antes da trava, com nova conferência depois, fica como evolução se a
  contenção aparecer.
- A persona consome tokens para ler artefatos: `hanko_artifact_get` respeita o teto por arquivo e a prévia de texto é
  limitada como na tela.
- O `Down` da migration volta ao modelo de cadeia sem apagar nada: renumera o ordinal por (task, nome) em ordem de
  criação. Documentos que atravessam tasks ou que dividem nome no mesmo escopo se fundem por cadeia, e MAJOR.MINOR, tags
  e identidade se perdem.
- A medida é heurística. Os limiares (0,10 / 0,40) estão em `domain` como constantes nomeadas e podem virar
  `instance_settings` se o dogfood pedir.
- Fora de escopo: aplicar MAJOR.MINOR às revisões append-only de instruções, skills e fluxos (ADR-0052). O padrão é o
  mesmo e pode ser estendido depois, com ADR próprio.

## Plano de entrega
1. **Contrato (esta sessão):** este ADR, a migration `00201` e os tipos e regras puras de domínio com testes.
2. Store (queries sqlc) + service (`PutArtifact` por documento, medida, restaurar, reclassificar, tags, permissão da
   persona) + fatos.
3. Trilhas paralelas sobre o contrato: MCP + briefing · API + OpenAPI + CLI · UI · export/import.
