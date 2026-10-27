# ADR-0088 — Export da empresa: o que dos dados externos viaja
Data: 2026-10-26 · Status: aceita (Bardi, 2026-10-26) · Issue: #66 (v0.8) · Usa: ADR-0022, ADR-0062, ADR-0075, ADR-0076, ADR-0077, ADR-0078, ADR-0082, ADR-0083 · Altera: ADR-0078

## Contexto
O export serve para levar um time de personas de uma instalação para outra, ou guardar um modelo de empresa. O backup
é o dump do banco (ADR-0022, "Contexto"; ADR-0062). A importação cria uma empresa nova, pausada, com ids novos
(ADR-0022 decisão 6).

O formato é um único JSON, `hanko.company.v1`, sem ids e sem segredos (ADR-0022 decisões 1 a 3). A importação lê até
32 MiB de uma vez e aceita no máximo 2 ao mesmo tempo (ADR-0022 decisão 9). Execuções e logs entram só com
`--with-runs` e `--with-logs` (decisão 4). Alertas, gates e orçamentos consumidos "não viajam: são estado de operação"
(ADR-0022, "Consequências"). O `v1` aceita campo opcional novo, como o `artifacts` do adendo do ADR-0028.

Os ADRs da #66 dizem que três grupos de dados externos entram no export, sem dizer o quê:
- agentes externos, emparelhamentos e o workspace `no-repo` (ADR-0075, "Consequências");
- hosts externos, com o formato "para o contrato do v0.8" (ADR-0077, "Consequências");
- conversas e sessões externas (ADR-0078, "Consequências").

Os ADR-0080 a 0083 não falam de export. Com 36 meses são ~197 mil conversas só do estado
(`docs/research/2026-10-banco-telemetria.md` §6), fora as chamadas ao modelo, e o dado bruto pesa ~126 GB por ano
(mesmo documento, §2): nada disso cabe no arquivo único de 32 MiB.

A escolha abaixo é do Bardi, em 2026-10-26.

## Decisão
1. **Viaja sempre, como configuração.**
   - Agentes externos, emparelhamentos e o workspace `no-repo` (ADR-0075).
   - Hosts externos, sem chave (ADR-0077).
   - A chave externa da assinatura e o histórico de mensalidade dela (ADR-0082 itens 4 e 6), junto das assinaturas e
     períodos que o export já leva.
   - Os limites de alerta da empresa (ADR-0083 item 3).
2. **Não viaja.**
   - Conversas e sessões externas, rodadas externas, aprovações externas e chamadas externas, com o custo calculado.
   - Alertas e itens da Caixa.
   - Preços de modelo, que são globais (ADR-0022 decisão 3).
   - A telemetria bruta: spans, logs e métricas.
3. **Sem segredos.** As chaves `ingest` e `read` são API keys, e API keys nunca entram no export (ADR-0022 decisão 3). O
   host importado vem sem chave: precisa de uma chave `ingest` nova antes de mandar telemetria (ADR-0076,
   "Consequências"). Os limites de alerta e a mensalidade são números, sem credencial.
4. **O passado tem outros caminhos.** O histórico do agent-studio chega pela migração da #66, com o ensaio que confere
   100% (ADR-0082 item 8). Depois, o banco e o bucket têm o backup do ADR-0062. O export não é backup.
5. **Formato.** O arquivo segue `hanko.company.v1`. Os campos novos são opcionais e por referência, sem id (ADR-0022
   decisão 2), e a importação os valida por inteiro antes de gravar (decisão 6). Os nomes e a ordem dos campos ficam
   para o contrato do v0.8. O round trip export → import → export tem de dar o mesmo arquivo, como no ADR-0022.
6. **Altera o ADR-0078.** A frase "Conversas e sessões externas entram … no export" deixa de valer; elas ficam fora,
   pelo item 2. Os ADR-0075 e 0077 continuam como estão, porque só tratam de configuração.

## Consequências
- Quem exporta a Oute para montar uma instalação de teste leva o time, os hosts e as assinaturas, e nenhuma conversa
  antiga. A empresa nova começa sem histórico.
- Quem precisar levar o histórico para outra instalação não tem como pelo export. Isso vira um ADR próprio, com tamanho
  máximo e formato.
- O export da v0.8 fica pequeno: acrescenta só configuração, e o teto de 32 MiB do ADR-0022 não muda.

## Alternativas descartadas
- **Configuração e um resumo do histórico (`--with-external`)**: um resumo numa empresa que nunca rodou tem pouco uso, e
  exige campos novos, teste de round trip e regra de tamanho.
- **Tudo, inclusive o histórico**: exigiria importação em fluxo e teto maior, ou um `hanko.company.v2`. Mexe em um ADR
  que funciona.
