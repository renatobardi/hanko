# ADR-0029 — Skills e integrações
Data: 2026-10-15 · Status: aceita

## Contexto
Duas telas do menu lateral eram "Em breve": **Skills** e **Integrações**. Uma persona só sabia o que estava nas suas
instruções, e dois pedidos recorrentes não tinham resposta: reutilizar a mesma habilidade (um checklist de revisão, um
procedimento de deploy) em várias personas sem copiar texto, e ligar o time a serviços de fora (um servidor MCP da
empresa para os agentes; um aviso para os sistemas da empresa quando algo acontece no Hanko).

Os dois assuntos pedem o mesmo cuidado: eles mexem com o que roda na máquina do servidor e com credenciais de terceiros.
Por isso a decisão trata, junto, de entrega, limites, segredos e SSRF.

## Decisão

### A. Skills
1. **O que é.** Uma skill é um `SKILL.md`: frontmatter com `name` (o slug, que é o nome que o harness aceita) e
   `description` (quando usar) e um corpo em markdown. Tabelas `skills` (por empresa, slug único, `version` sobe a cada
   mudança de nome, descrição ou corpo) e `persona_skills` (a ligação persona × skill, com `enabled`). Colar um
   `SKILL.md` inteiro no corpo vale: o service absorve o frontmatter e completa nome e descrição.
2. **Entrega por harness, a cada run.**
   - `claude`: o Hanko monta um diretório temporário com `.claude/skills/<slug>/SKILL.md` e o passa em `--add-dir`; o
     Claude Code carrega skills de diretórios adicionais e decide sozinho quando usar cada uma (carga sob demanda). O
     diretório é apagado no fim da run.
   - `codex` e `acp`: não têm o mecanismo de skills. O prompt de sistema recebe um **índice** (slug e descrição) e o corpo
     é buscado pela tool MCP `hanko_skill_get` (há também `hanko_skill_list`). A tool só devolve skills habilitadas para
     a persona que chama, pelo run token.
3. **Limites.** 32 KiB por skill; 120 caracteres de nome; 400 de descrição; por run, no máximo 50 skills e 256 KiB no
   total. O que não cabe fica de fora e a run registra um evento de sistema dizendo quantas foram cortadas, em vez de
   falhar.
4. **Slugs reservados.** `nova` e `preview` são rotas fixas da UI e não valem como slug de skill nem de integração.

### B. Integrações
5. **Tipos.** `integrations` guarda dois tipos. **MCP da empresa**: transporte `http` (URL e headers) ou `stdio`
   (comando, argumentos e variáveis de ambiente), valendo para todas as personas ou só para as ligadas
   (`integration_personas`); entra no `mcpServers` da run ao lado do Hanko e da memória, e um nome do usuário nunca
   substitui os servidores do Hanko. **Webhook de saída**: URL https, segredo HMAC e lista de eventos
   (`task.created`, `task.completed`, `gate.opened`, `gate.decided`, `run.failed`, `alert.opened`).
6. **stdio é decisão do operador.** Um MCP stdio executa um comando na máquina do servidor. Só funciona com
   `HANKO_INTEGRATIONS_ALLOW_STDIO=true` (padrão desligado); sem isso a criação é recusada e uma integração stdio já
   existente é ignorada na run. No codex o comando e os argumentos vão na linha de comando e as variáveis por nome
   (`env_vars`), com os valores só no ambiente do harness.
7. **Entrega de webhook.** Um `POST` JSON por evento, com `X-Hanko-Signature: t=<unix>,v1=<hex>` (HMAC-SHA256 de
   `<t>.<corpo>` com o segredo), `X-Hanko-Event` e `X-Hanko-Delivery` (o id, para o receptor deduplicar). Cada entrega
   é uma linha em `webhook_deliveries` e um job do river **enfileirado na mesma transação do fato** (outbox
   transacional: sem fato não há entrega, e uma queda depois do commit não perde o aviso). Timeout de 10 s por tentativa.
   Retry exponencial: 7 tentativas no total, com espera de 30 s multiplicada por 4 e teto de 6 h. 2xx é sucesso; qualquer outra resposta ou erro
   de rede tenta de novo. Destino que a proteção SSRF recusa, integração desligada e segredo ilegível encerram a entrega
   sem retry. O histórico (as últimas 100 entregas por webhook: código HTTP, latência, tentativas, próxima tentativa, erro) aparece
   na UI, na API e na CLI. O botão/comando **testar** manda um evento `ping` assinado, sem retry; para MCP http faz o
   `initialize` do protocolo.
8. **Proteção SSRF.** O destino de um webhook é validado na criação (https, sem usuário e senha) e **de novo na
   conexão**: a checagem do IP fica no `Control` do `net.Dialer`, depois da resolução de DNS, o que fecha o rebinding.
   Loopback, link-local (inclui 169.254.169.254), redes privadas (RFC 1918, ULA), CGNAT e multicast são recusados;
   redirecionamentos não são seguidos. `HANKO_INTEGRATIONS_ALLOW_PRIVATE=true` (padrão desligado) libera destinos
   internos para quem integra com sistemas da própria rede. O pacote é `internal/netguard`.
9. **Segredos cifrados em repouso.** Headers e variáveis de ambiente do MCP e o segredo HMAC do webhook são guardados
   cifrados com **AES-256-GCM** (`internal/secretbox`), chave derivada de `HANKO_SECRET_KEY`. Formato
   `v1:<id da chave>:<base64>`; o id da integração entra como dado autenticado (AAD), então um segredo copiado para
   outra linha não abre. Sem `HANKO_SECRET_KEY` o servidor sobe, mas **recusa guardar segredos**.
   **Rotação**: defina a chave nova em `HANKO_SECRET_KEY` e a antiga em `HANKO_SECRET_KEY_PREVIOUS`, reinicie e rode
   `hanko integration rekey` (ou `POST /api/v1/integrations/rekey-secrets`, só dono da instalação): os segredos são
   reescritos com a chave atual; depois disso a variável antiga pode sair.
10. **Segredo nunca volta.** A API, a UI, a CLI, o export, os eventos e os logs só mostram **que há segredo**
    (`secret_set`) e, no webhook gerado pelo servidor, um prefixo inofensivo (`secret_hint`); do MCP só os **nomes** dos
    headers e variáveis. Os valores também passam pela redação de segredos dos eventos da run (`adapter.Redact`
    conhece os valores guardados). **Exceção documentada, mostrar uma vez:** quando o servidor gera o segredo do webhook
    (criação sem `secret`, ou rotação), ele aparece na resposta daquela chamada, com aviso, e não há como lê-lo de novo.

### Transversal
11. **Papéis (ADR-0021).** Admin e owner criam, alteram, apagam, ligam e testam; member e viewer só leem (a UI esconde os
    controles, o service recusa de qualquer modo). Os endpoints de listar e ler nunca incluem segredo. A rotação de
    chave é do dono da instalação.
12. **API, CLI, MCP e UI.** `/api/v1/companies/{c}/skills…` e `/integrations…`; `hanko skill …` e `hanko integration …`;
    telas `/skills` (lista, editor com pré-visualização markdown sanitizada, ligar por persona) e `/integracoes`
    (lista, criar, testar, entregas, ligar); na página da persona, painéis de skills e de servidores MCP.
13. **Export/import (ADR-0022).** O `hanko.company.v1` ganha campos **opcionais**: `skills`, `personas[].skills` e
    `integrations`. Integrações saem **sem segredos**: sem valores de header e variável, sem segredo HMAC e sem argumentos
    de stdio; a URL do MCP perde usuário, query e fragmento, e a do webhook fica só com esquema e host. Na importação
    elas entram **desligadas**, mesmo com `--active`, e quem importa informa os segredos de novo. Um arquivo sem os
    campos continua válido.

14. **Runs de sistema e ACP.** A curadoria de memória (gatilho `memory`, ADR-0030) e a revisão automática NÃO recebem
    skills nem MCPs da empresa: são runs de sistema com protocolo próprio. O adapter `acp` recebe as skills pelo índice
    no prompt (como o codex) e os MCPs da empresa pelo `mcpServers` da sessão; o MCP http só chega se o agente
    anunciar `mcpCapabilities.http` (o que o adapter já filtra, com aviso na run).
15. **Webhooks e a curadoria.** As propostas de memória usam verbos próprios (`memory.proposal_*`) e NÃO geram
    `gate.opened`/`gate.decided`: esses eventos são só dos gates `approval`, `task_review` e `hire`. A run de curadoria
    segue as regras comuns (`run.failed` se falhar; a task de sistema dela emite `task.completed`).

## Consequências
- Personas reaproveitam habilidades sem copiar texto, e a mesma skill chega ao Claude como skill de verdade e ao
  Codex/ACP pelo índice e pela tool MCP. A carga sob demanda no Codex depende de o agente chamar `hanko_skill_get`;
  ele pode não chamar.
- Skills são texto injetado no contexto do agente: quem administra a empresa controla o que o agente lê. Por isso só
  admin edita, e o corpo tem teto.
- O fluxo de webhook é **pelo menos uma vez**: um retry depois de um 2xx perdido entrega duas vezes; o receptor deduplica
  por `X-Hanko-Delivery`. Não há ordem garantida entre eventos.
- Um webhook que falha para sempre gera 7 tentativas e para; o histórico fica na tela. Não há desligamento automático.
- Perder `HANKO_SECRET_KEY` perde os segredos guardados (as integrações continuam, sem credenciais). A chave deve estar
  no mesmo cofre do `HANKO_SECRET` e no backup do operador, nunca no banco.
- O reveal-once do segredo gerado é uma exceção deliberada à regra de nunca devolver segredo; o custo é o usuário
  precisar guardá-lo, o benefício é nunca precisar guardar o segredo em claro no servidor.
- O teste de MCP http é feito pelo servidor, passando pela proteção SSRF, mas a conexão de verdade da run parte do
  agente, na máquina do servidor, sem essa proteção (o destino de um MCP pode ser interno de propósito).
- stdio e destinos privados são interruptores do operador, não da empresa: uma empresa comprometida não os liga sozinha.
