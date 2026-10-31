# ADR-0089 — Assistente "Conectar ferramenta a uma persona"
Data: 2026-10-31 · Status: proposta (aguarda o gate do Bardi) · Issue: #45 · Usa: ADR-0029, ADR-0036, ADR-0043, ADR-0053, ADR-0067

## Contexto
Ligar um servidor MCP remoto a uma persona, com a escrita sob controle, passa hoje por sete telas em ordem certa: integração,
catálogo de tools, perfil, regras por risco, vínculos, interruptor da persona e o teste da política (#45). O ADR-0067
corrigiu os três padrões que abriam demais por omissão, mas o fluxo continua longo.

A #45 pede um assistente em uma tela (UI) e um comando (`hanko integration connect`) que orquestram serviços que já existem.
As escolhas abaixo são do Bardi, na conversa de 2026-10-31 (comentário na #45); os três pontos finais são recomendações do
autor, aceitas por ele ("suas recomendações"), e o ponto 1 ganhou um ajuste depois da leitura do código (ver "Ajuste").

O que o código já tem (fonte: `internal/service@538a31d`):
- `CreateIntegration`, `CreateToolProfile`, `BindToolProfile` e `SetIntegrationToolChoices` exigem papel admin
  (`integrations.go:410`, `tools_profiles.go:106`, `:479` e `:555`).
- A descoberta de tools (`refreshCatalog`) recebe uma integração **já gravada** (`tools_catalog.go:198`): não existe
  descoberta sem integração.
- Integração MCP nova nasce com a escrita em quarentena (ADR-0067, decisão 2).

## Decisão
1. **Um serviço de orquestração, três portas.** A UI, a API e a CLI `hanko integration connect` chamam o mesmo serviço.
   Marco único: serviço, API, CLI e UI juntos.
2. **Cinco passos.**
   1. Servidor: URL e credencial.
   2. Testar e descobrir: chama `tools/list` e mostra as tools por risco (as de escrita já em quarentena, ADR-0067).
   3. Quem usa: escolha de personas, **nunca "todas" por padrão**.
   4. Modelo de acesso.
   5. Conferir: a tabela "Tools efetivas" das personas escolhidas, antes de concluir.
3. **Credencial: header com segredo e conexão OAuth 2.1 com PKCE** (ADR-0043), os dois desde o primeiro marco. Vale o que o
   ADR-0043 já define para a conexão (consentimento no navegador, renovação, setup recuperável).
4. **Modelos de acesso são dados por empresa, não código.**
   - Cada modelo diz, para cada nível de risco da tool (`read`, `write`, `destructive`), um modo do ADR-0053: `allow`, `ask`
     ou `off`.
   - Nascem três por empresa: **Somente leitura** (`read` allow; `write` e `destructive` off), **Leitura livre, escrita
     com aprovação** (`read` allow; `write` e `destructive` ask), o padrão sugerido, e **Personalizado**.
   - **Personalizado não é um modelo guardado:** abre o editor de perfil que já existe. Não vira um terceiro tipo de dado.
5. **Quem edita:** admin da empresa cria, edita e apaga os modelos (como os perfis de tools de hoje). Member não roda o
   assistente, porque as operações que ele orquestra exigem admin.
6. **Copiar, não vincular.** Ao concluir, o assistente grava o perfil e as regras por risco da integração a partir do
   modelo, e liga o perfil às personas escolhidas. Editar o modelo depois **não muda** as conexões já feitas. Mudar a
   permissão de uma conexão existente é explícito, pelo editor de perfil.
7. **Nada libera escrita sem escolha explícita.** Só sai da quarentena o que o modelo escolhido permite. O passo 5 mostra o
   que cada persona poderá fazer antes de concluir.
8. **Dados iniciais.** Uma migration cria os três modelos para toda empresa que já existe, e o serviço que cria empresa
   nova faz o mesmo, com `activity_event` na mesma transação (`AGENTS.md`).
9. **Toda escrita do assistente gera `activity_event` na mesma transação** (`AGENTS.md`).

### Ajuste: quando a falha do `tools/list` deixa algo gravado
A recomendação inicial dizia "parar sem criar nada". O código não deixa fazer isso por inteiro: a descoberta precisa de uma
integração gravada (`tools_catalog.go:198`). Fica assim:
10. **O passo 2 grava a integração, e só ela.** Ela nasce fechada para escrita e **sem vínculo com nenhuma persona**
    (ADR-0067 decisões 1 e 2), então não abre acesso a ninguém. O perfil, as regras por risco e os vínculos são gravados
    juntos, numa transação, só no passo de concluir.
11. **Se o `tools/list` falhar** (servidor fora do ar, token errado, OAuth sem consentimento), o assistente para nesse passo,
    mostra o erro e oferece **tentar de novo** ou **descartar**. Descartar apaga a integração que o próprio assistente
    criou. Se a pessoa fechar a tela sem escolher, a integração fica na lista, fechada e sem vínculo, como o caso de borda do
    ADR-0067 ("uma integração cuja primeira descoberta falhou já tem linha de catálogo, com o erro").

## Alternativas descartadas
- **Modelos fixos no código:** simples, mas a regra de "leitura livre" e "escrita com aprovação" varia por empresa.
- **Vínculo vivo entre o modelo e as conexões:** uma edição do modelo poderia liberar escrita de personas em uso sem
  ninguém escolher.
- **Só o header na primeira versão, OAuth depois:** teria deixado o marco menor. O Bardi escolheu os dois juntos.
- **Gravar tudo só no fim, sem integração prévia:** não funciona hoje, porque a descoberta exige a integração gravada.
- **"Personalizado" como modelo guardado:** aumenta o cadastro para um caso que o editor atual já cobre.

## Consequências
- Marco grande: serviço, API, CLI, UI, OAuth e a migration dos modelos entram no mesmo aceite. Se na hora de planejar as
  fatias isso pesar, a decisão 3 (OAuth no primeiro marco) é a primeira a reabrir.
- O contrato (migration, tipos de domínio e OpenAPI) vem depois deste ADR, com gate do Bardi, antes de qualquer código.
- Uma integração órfã pode ficar na lista quando a pessoa abandona o assistente depois do passo 2. Ela não abre acesso.

## O que não foi decidido
- O nome das tabelas e o formato exato da API (do contrato).
- A tela de retorno do OAuth dentro do assistente: como ela volta ao passo certo.
