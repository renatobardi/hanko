# ADR-0084 — Gerador único de nome: inglês, 256 × 256, listas como dado do contrato
Data: 2026-10-25 · Status: proposta (aguarda aceite do Bardi) · Issue: #66 (v0.8) · Usa: ADR-0078, ADR-0080

## Contexto
Há três geradores de nome amigável no formato `Adjetivo_Substantivo`, cada um com uma regra própria:

- **Run do Hanko.** Usa palavras em pt-BR, 64 × 64. O nome sai do id. Se já existe na empresa, o gerador tenta até 16
  variações e depois acrescenta um número de 3 dígitos. O nome fica gravado no run. As listas fazem parte do contrato
  (`internal/domain/runname.go:10-64`). `CanonicalRunName` reconhece o nome digitado sem acento ou em outra caixa, e o
  CLI e a busca de runs dependem dele (`runname.go:85-105`; `internal/cli/run.go:213`; `internal/service/runs.go:56`).
- **Conversa no agent-studio.** Usa palavras em inglês, 64 × 64. O nome sai do `session.id` e não é gravado. Dois ids
  podem cair no mesmo nome, e por isso a tela mostra o id junto (`agent_studio/names.py:1-4,22-26@845764f` do
  oute-agent).
- **Rodada.** O `oute-swarm` sorteia um nome entre os que nenhuma rodada aberta usa e o grava no `meta` da rodada. Para
  isso, chama o `names.py` do agent-studio (`names.py:6-7,29-32@845764f`; `docker/oute-swarm:203-204@845764f`). Ele roda
  no oute, fora do Hanko.

No gate da #67, o Bardi decidiu (`docs/research/2026-10-inventario-agent-studio.md` §9.1 ponto 7):
- um gerador só, em inglês, com 256 × 256 = 65.536 combinações;
- nome que já existe não muda;
- o `names.py` sai do agent-studio antes do corte;
- as 192 + 192 palavras novas vão num PR para a revisão dele.

A conversa externa ganha o nome do gerador único e guarda o nome migrado (ADR-0078). A escolha abaixo é do Bardi, em
2026-10-25.

## Decisão
1. **Listas como dado do contrato.** As 256 palavras de cada lista ficam num arquivo de dados versionado no repo do
   Hanko; o caminho fica no contrato do v0.8. Os adjetivos vêm primeiro e os substantivos depois (animal ou pessoa,
   como hoje no oute). Cada palavra só tem letras ASCII, com maiúscula na primeira, e casa com
   `^[A-Z][a-z]+_[A-Z][a-z]+$`, o formato que a rodada já usa (`docker/oute-swarm:205@845764f`). Mudar uma palavra é
   mudar o contrato.
2. **Duas regras, escritas junto com as listas.**
   - **Derivado do id**, para runs e conversas externas: o nome sai do sha256 do id. Se já existe na empresa, o gerador
     tenta as variações seguintes e, no fim, acrescenta o número de 3 dígitos, que só existe aqui. É a regra de
     `runname.go` de hoje. O nome fica
     gravado.
   - **Sorteio entre os livres**, para a rodada: um nome que nenhuma rodada aberta usa, como no `draw` de hoje.
3. **Cópia no oute-agent.** O oute-agent guarda uma cópia do arquivo, com a versão do Hanko fixada. Um teste confere a
   cópia contra a do Hanko nessa versão. O `oute-swarm` sorteia sozinho, sem chamar o Hanko: a rodada abre mesmo com o
   Hanko fora do ar. O `names.py` sai do agent-studio antes do corte, num PR no oute-agent.
4. **Nome existente não muda.** Os runs gravados, as rodadas abertas e as conversas migradas guardam o nome que têm.
   As listas pt-BR de hoje ficam só para reconhecer nomes antigos: `CanonicalRunName` aceita o nome antigo e o novo, e
   o gerador nunca as usa de novo.
5. **Palavras novas.** As 192 + 192 palavras novas, somadas às 64 + 64 do oute, vão num PR próprio para a revisão do
   Bardi, antes do código do gerador.

## Consequências
- O espaço passa de 4.096 para 65.536 nomes; a colisão na mesma empresa fica rara, e a tentativa seguinte resolve.
- O oute-agent passa a depender de um arquivo do Hanko; atualizar as listas exige atualizar a cópia, e o teste
  aponta a diferença.
- O nome de um run antigo, digitado sem acento, continua achando o run.

## Alternativas descartadas
- **O Hanko sorteando o nome da rodada pela API**: a rodada não abriria com o Hanko fora do ar, e uma chave de leitura
  passaria a escrever.
- **Nome da rodada derivado do id da rodada**: duas rodadas abertas poderiam ter o mesmo nome, e o `oute-swarm` só
  saberia o nome depois.
- **Conversa com nome calculado, sem gravar**, como hoje no agent-studio: o nome mudaria se as listas mudassem.
