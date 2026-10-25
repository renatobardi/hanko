# ADR-0086 — Direção: o oute dentro do Hanko pela mesma ingestão, e a memória num ai-memory só
Data: 2026-10-25 · Status: aceita (Bardi, 2026-10-25) · Issue: #66 (v0.8) · Usa: ADR-0015, ADR-0075, ADR-0076, ADR-0077, ADR-0078, ADR-0080, ADR-0081

## Contexto
A decisão 7 da #66 deixa fora deste projeto trazer o oute para dentro do Hanko. O nome futuro é "Hanko CLI", mas ele
colide com o binário `hanko` que já existe (`cmd/`), por isso fica para esse projeto ("Em aberto" da #66). A mesma
decisão pede um ADR de direção que garanta que os conceitos da decisão 6 sirvam ao projeto futuro sem nova migração.

A decisão 15 deixa a memória fora: o oute mantém o ai-memory dele e o Hanko o seu. A união fica para este ADR. O ai-memory
do Hanko é o provider padrão. Toda escrita passa pela `memory_outbox`. A fronteira é: recall no ai-memory, governança no
Postgres; o ai-memory nunca é fonte de verdade de estado (ADR-0015 itens 1, 4 e 5). O oute escolhe `workspace` e
`project` pelo `.ai-memory.toml` de cada repo.

Os conceitos da decisão 6 já estão nos ADRs:
- agente externo (ADR-0075);
- ingestão (ADR-0076);
- host externo (ADR-0077);
- conversa e sessão externas (ADR-0078);
- rodada externa (ADR-0080);
- aprovação externa (ADR-0081).

As escolhas abaixo são do Bardi, em 2026-10-25.

## Decisão
1. **A ingestão é o contrato.** O oute dentro do Hanko continua mandando os mesmos eventos OTLP para a mesma ingestão,
   com a mesma dedupe (ADR-0076). Ele não ganha uma segunda porta para os mesmos fatos.
2. **Origem desde já.** Cada conceito da decisão 6 nasce no v0.8 com um campo `source_kind`, de valor `external`. O
   projeto futuro acrescenta outro valor; nada muda de tabela nem de id, e o histórico não se migra de novo.
3. **Telas e API não dependem da origem.** Elas leem os conceitos pelo que são, não por serem externos. A origem só
   serve de filtro.
4. **Memória: nada muda no v0.8.** Até a união, nenhum lado lê a memória do outro.
5. **Rumo da união da memória.** Quando o oute rodar dentro do Hanko, a memória dele passa a ser servida pelo provider
   do Hanko, num ai-memory só.
   - A importação leva cada `workspace`/`project` do `.ai-memory.toml` ao workspace emparelhado do repo (ADR-0075).
   - A fronteira do ADR-0015 vale também para essa memória.
   - A união é um projeto próprio, com ensaio e conciliação como os da #66.
6. **Fica para o projeto futuro:** o nome do produto e como o binário convive com o `hanko` de hoje.

## Consequências
- O v0.8 grava um campo que, por enquanto, só tem um valor; em troca, a entrada do oute no Hanko não reescreve dados.
- Enquanto a união não acontecer, quem trabalha no oute e no Hanko consulta duas memórias.

## Alternativas descartadas
- **O oute dentro do Hanko escrevendo direto no domínio**: seriam duas portas para os mesmos fatos, e a mudança exigiria
  converter o histórico externo.
- **Nenhuma garantia agora**: não cumpre o que a decisão 7 pede.
- **O Hanko lendo a memória do oute já no v0.8**: o Hanko guardaria acesso a um segundo servidor, e o escopo do v0.8
  cresceria.
- **Memórias que nunca se juntam**: o oute dentro do Hanko seguiria com uma memória à parte.
