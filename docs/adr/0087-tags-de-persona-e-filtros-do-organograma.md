# ADR-0087 — Tags de persona e filtros do organograma por tag e por nível
Data: 2026-10-26 · Status: proposta (aguarda aceite do Bardi) · Issue: #118 · Usa: ADR-0005, ADR-0021, ADR-0022, ADR-0024, ADR-0031 · Não muda: ADR-0064

## Contexto
Hoje, só os documentos de artefato têm tags. Elas são texto livre num `text[]` do próprio documento, com até 20 por
documento, e não têm cadastro (ADR-0064; `db/migrations/00201_documentos_de_artefato.sql:12`). Personas não têm tags.

O organograma é a floresta de `personas.reports_to`. Uma persona sem superior, ou cujo superior não existe mais, vira
raiz (`internal/service/org.go:28`). A ilha `ui/islands/src/org-chart/OrgChart.tsx` só recolhe e expande ramos. Não
tem filtro nem escolha de nível.

O Bardi quer criar, editar, arquivar e deletar tags de persona, aplicar essas tags em Personas e no organograma, e
filtrar o organograma por tag e por nível. As escolhas abaixo foram feitas por ele, uma a uma, na sessão de grill de
2026-10-26, e estão registradas na #118.

## Decisão
1. **Cadastro de tags por empresa, só para personas.** A tag é uma entidade com `id`, nome, cor, descrição e estado
   (ativa ou arquivada). Ela se liga a personas numa tabela própria, com uma linha por par persona ↔ tag. As tags de
   artefato continuam como texto livre (ADR-0064). Unificar os dois tipos de tag exige um ADR novo.
2. **Campos e limites.**
   - **Nome:** de 1 a 40 caracteres, sem espaço no começo nem no fim. É único na empresa sem distinguir maiúscula de
     minúscula, comparado depois de `lower`. Aceita acento e espaço, porque é um rótulo de cadastro e não um slug.
   - **Cor:** um nome de uma paleta fixa de tokens do Kubo DS, cuja lista fica no contrato. Não aceita hex livre, para
     que a cor funcione nos temas claro e escuro (ADR-0031).
   - **Descrição:** opcional, até 200 caracteres. Aparece no tooltip do chip.
   - **Identidade:** renomear ou trocar a cor mantém o `id`, e as ligações não mudam.
   - **Limites:** até 20 tags por persona e até 200 tags por empresa, contando as arquivadas. O service confere os
     dois limites e devolve erro de validação, sem cortar nada.
3. **Arquivar e deletar.**
   - **Arquivar** se desfaz. Uma tag arquivada não pode ser aplicada a mais nenhuma persona e não aparece no seletor
     nem na lista padrão do filtro. Ela continua nas personas que já tinham, com o chip esmaecido, e o filtro a mostra
     com "mostrar arquivadas". Desarquivar devolve a tag ao estado ativo.
   - **Deletar** apaga a tag e todas as ligações dela, e não se desfaz. Antes de confirmar, a tela mostra quantas
     personas perdem a tag. O evento de exclusão guarda o nome da tag e essa contagem. A exclusão não gera um evento
     por persona afetada.
4. **Papéis (ADR-0021).** A autorização fica no service.
   - `viewer` vê as tags e usa os filtros.
   - `member` cria e edita tags, e aplica e tira tag de uma persona.
   - `admin` arquiva, desarquiva e deleta.
5. **Telas.**
   - **Cadastro:** uma aba "Tags" dentro de Personas, com criar, editar, arquivar, desarquivar e deletar. O
     organograma tem um link "Gerenciar tags" para essa aba.
   - **Aplicar e tirar tag:** na ficha da persona e no menu do nó do organograma. No seletor, digitar um nome que não
     existe oferece "criar tag «nome»", e a tag é criada e aplicada numa transação só.
   - **Exibir:** chips coloridos no card da lista de Personas e no nó do organograma, com no máximo 3 visíveis e
     "+N" para o resto.
   - **Edição em lote** (seleção múltipla na lista de Personas, "aplicar tag" e "tirar tag"): fica para a segunda fatia.
     Ela usa as mesmas regras e os mesmos eventos, com um evento por persona alterada.
6. **Nível no organograma.** O nível é calculado e não fica gravado. **Nível 1** é toda raiz do organograma, ou seja,
   uma persona sem `reports_to` ou cujo superior não existe mais (`org.go:28`). O nível de cada persona é o do
   superior mais um, qualquer que seja o status do superior. `OrgChart.Depth` já conta os níveis
   (`internal/service/org.go:25`).
7. **Filtros do organograma.**
   - **Tag:** uma ou mais tags, com OU como padrão e uma chave para E. Há também o critério "sem tag". Tags arquivadas
     só entram na lista com "mostrar arquivadas".
   - **Nível:** dois controles, que podem ser usados juntos. A **profundidade máxima** recolhe tudo abaixo do nível N.
     A **seleção de níveis** marca um ou mais níveis.
   - **Combinação:** a tag e a seleção de níveis se combinam por E. A profundidade máxima corta a árvore antes desses
     dois critérios.
   - **Modo**, escolhido no filtro:
     - **Destacar:** a árvore inteira, dentro da profundidade máxima, fica na tela. As personas fora do recorte
       ficam esmaecidas.
     - **Podar mantendo o caminho:** só aparecem as personas do recorte e os superiores delas até a raiz. Os
       superiores que estão fora do recorte aparecem esmaecidos, só como caminho.
   - **Recorte vazio:** a tela mostra "nenhuma persona nesse recorte" e não bloqueia a combinação.
8. **Guardar o filtro**, igual às tasks (ADR-0031, item 4). Os filtros, o modo e a profundidade são parâmetros da URL,
   então um link filtrado abre o mesmo recorte para outra pessoa. A última escolha fica no cookie `hanko_org`. Não há
   visões salvas com nome. A ilha do organograma continua sendo uma só, e o teto de 3 ilhas do ADR-0005 não muda.
9. **API, CLI e MCP.**
   - **API e CLI:** todas as operações: CRUD do cadastro, arquivar e desarquivar, aplicar e tirar tag, e listar
     personas por tag e por nível. O contrato define as rotas, os comandos e os nomes dos parâmetros.
   - **MCP:** só leitura. `hanko_persona_list` (`internal/mcp/tools.go:322`) passa a devolver as tags de cada persona
     e aceita um filtro por tag, para o agente achar a quem delegar. `hanko_whoami` devolve as tags da própria
     persona. Nenhuma tool cria, edita ou aplica tag: classificar cabe ao humano, como no ADR-0064.
   - **Briefing do despacho:** não muda.
10. **Busca (ADR-0024).** Os nomes das tags entram na busca da persona. `personas.search_tsv` é uma coluna gerada e não
    pode ler outra tabela (`db/migrations/00024_busca_dobra_formatacao.sql:114`). Por isso, a persona ganha uma coluna
    com os nomes das suas tags, atualizada pelo service na mesma transação de aplicar, tirar, renomear ou deletar uma
    tag. O `search_tsv` passa a incluir essa coluna, com peso B. O contrato define o nome da coluna.
11. **Export e import (ADR-0022).** O pacote da empresa leva o cadastro (com as tags arquivadas) e as ligações. Na
    importação, uma tag com o mesmo nome, sem distinguir caixa, de uma tag que já existe passa a ser essa tag, e não
    vira uma duplicata.
12. **Eventos.** Toda escrita gera um `activity_event` na mesma transação (`AGENTS.md`):
    - `persona_tag.created`, `persona_tag.updated` (nome, cor ou descrição), `persona_tag.archived`,
      `persona_tag.unarchived` e `persona_tag.deleted` (com o nome e a contagem de personas afetadas);
    - `persona.tagged` e `persona.untagged`, uma por persona.

    Esses nomes foram propostos pelo autor no resumo final da sessão. O Bardi aceitou o resumo, mas não discutiu este
    item em separado.
13. **Entrega.** É uma trilha paralela à v0.7 e à v0.8, porque não toca nas tabelas de cofre nem nas de agente externo.
    A ordem é: este ADR, depois o contrato (migrations no próximo número livre da `origin/main`, tipos de domínio e
    OpenAPI), cada um com o gate do Bardi. Em seguida vêm duas fatias de código: a primeira com os itens 1 a 4 e 6 a 12
    e a parte de telas do item 5 sem o lote; a segunda com a edição em lote.

## Consequências
- Um nome de tag vale para todas as personas da empresa. Corrigir um erro de digitação muda o nome em todo lugar de
  uma vez.
- Deletar uma tag perde as ligações para sempre. O rastro que fica é o evento, com o nome e a contagem.
- A busca passa a depender de uma coluna mantida pelo service. Uma escrita de tag que não passe pelo service deixa
  essa coluna desatualizada, e um teste do service precisa cobrir os quatro caminhos do item 10.
- O agente consegue achar personas por tag, mas não consegue classificar ninguém.
- Os filtros e os níveis rodam na ilha, sobre a árvore que o host já envia. Não há uma consulta nova por filtro.

## Alternativas descartadas
- **Um cadastro único para personas e artefatos:** reabriria o ADR-0064 e mudaria o contrato MCP de
  `hanko_artifact_put` sem um caso que pedisse isso.
- **Texto livre na persona**, como no artefato: editar, arquivar e deletar virariam operações em massa sobre um texto,
  sem identidade, cor nem estado.
- **Esconder quem está fora do recorte, sem manter o caminho:** uma persona filtrada ficaria solta, sem o superior, e
  o organograma perderia a hierarquia.
- **Visões salvas com nome no banco:** pediriam uma tabela e telas novas, e a URL com o cookie já cobre o uso.
- **Deletar só uma tag já arquivada ou sem uso:** seria mais seguro, mas custaria um passo a mais. A confirmação com a
  contagem foi considerada suficiente.
