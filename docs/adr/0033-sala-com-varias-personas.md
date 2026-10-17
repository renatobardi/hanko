# ADR-0033 — Sala: uma conversa com várias personas
Data: 2026-10-16 · Status: aceita

## Contexto
O chat (ADR-0027) é uma conversa de um humano com **uma** persona. Para planejar a semana, revisar o roadmap ou destravar
uma task o time precisa de várias personas na mesma conversa — a reunião — sem que o humano copie a resposta de uma para
a outra. O menu já reservava "Sala" ("Em breve"). A sala não deve criar outro caminho de execução: fila, adapter,
orçamentos com hard stop, trava semanal, ledger, alertas, log ao vivo e retomada de sessão são os do chat. O risco novo é
dinheiro: duas personas que se citam conversariam para sempre.

## Decisão
1. **A sala é uma conversa.** `conversations.kind` `direct|room` (padrão `direct`, migration 00029). Numa sala
   `persona_id` é NULL (`conversations_kind_persona_check`: direta ⇒ persona; sala ⇒ sem persona) e as personas ficam em
   `conversation_participants (company_id, conversation_id, persona_id, added_at, added_by, removed_at)`, de 2 a 12 ativas;
   uma persona que sai e volta reaproveita a linha. `conversations.room_settings jsonb`: `mode` (`mentions` | `all`),
   `moderator_persona_id`, `max_hops` (1..5, padrão 2) e `max_runs_per_message` (0..24; 0 = número de participantes
   ativos). A moderadora efetiva é a configurada se ainda está na sala, senão a primeira participante; quando a moderadora
   sai, a próxima assume e isso fica na configuração.
2. **Mensagens e entregas.** `conversation_messages` ganha `persona_id` (quem falou, ou de quem um aviso trata),
   `reply_to_id` e `mentions uuid[]`. Uma mensagem da sala pode ser recolhida por várias runs, uma por persona:
   `conversation_message_runs (message_id, persona_id, run_id, origin_message_id, hop)` é a **entrega**; `run_id` NULL é a
   fila da persona. `conversation_messages.run_id` continua com o sentido do chat direto (a run que recolheu a mensagem
   humana) e, na sala, marca só a run que produziu a resposta ou o aviso.
3. **Uma run ativa por (conversa, persona).** `runs_one_active_per_conversation` vira
   `runs_one_active_per_conversation_persona (conversation_id, persona_id)`. O chat direto só tem uma persona, então
   continua com uma run por conversa. A linha da conversa (`FOR UPDATE`) segue como ponto de serialização de mensagens,
   fim de run e revisão.
4. **Quem responde.** Menções `@slug` (no começo ou depois de espaço/pontuação; e-mails e trechos de código não contam)
   só valem para personas **ativas da sala**; persona de fora nunca é acordada. Modo `mentions`: respondem as citadas; sem
   menção, a moderadora. Modo `all`: todas. Cada destino passa pelo despacho do chat (status da persona, orçamentos,
   trava semanal); com run da persona ativa na sala, a entrega espera na fila dela (até 20) e a próxima run recolhe tudo
   (coalescência por persona). Destinos além do teto `max_runs_per_message` ficam de fora com aviso.
5. **Recusa parcial.** A recusa de uma persona não derruba as outras: se ao menos uma aceitou, a mensagem é gravada e cada
   recusa vira uma mensagem de sistema com o motivo. Se nenhuma aceitou, a mensagem não é gravada e o erro diz por quê
   (o motivo único com o seu tipo — orçamento, trava, persona indisponível — ou os motivos juntos, 409). Como no chat, os
   efeitos da recusa (pausa por orçamento, alertas) são confirmados e uma sala criada com mensagem recusada é apagada sem
   deixar atividade.
6. **Persona acorda persona, com freio.** A resposta (resumo final da run) entra na thread com o nome da persona. As
   personas ativas que ela cita com `@slug` (nunca ela mesma; nenhuma, se ela já saiu da sala) são acordadas com a resposta
   como entrada. Cada entrega guarda a **origem** (a mensagem humana que abriu a cadeia) e o **salto** (0 = humano; 1 =
   resposta que acordou outra; ...). Uma run tem o maior salto do que recolheu e a origem da primeira entrega. A citação
   só acorda se `salto + 1 ≤ max_hops` e se as runs da cadeia (as que já recolheram uma entrega dela mais as personas com
   uma entrega dela na fila) ainda estão abaixo de `max_runs_per_message`; senão a sala recebe um aviso e ninguém roda.
   Run cancelada ou interrompida, ou sala arquivada, não acorda ninguém nem abre a próxima run.
7. **Prompt.** `BuildRoomPrompt`: instruções da persona + protocolo de sala (quem mais está na sala, com slug e cargo; se
   ela é a moderadora; responder só o que lhe cabe; citar `@slug` só quando outra persona precisa agir; o limite de saltos;
   não pedir confirmação ao humano para o que é reversível; irreversível vira task + gate). O prompt traz o histórico
   recente **com o nome de quem falou** (mesmo orçamento de 24 KiB e 4 KiB por mensagem do chat) e as mensagens que a
   run recolheu. A sessão do harness é retomada por (conversa, persona) quando o adapter sabe retomar.
8. **Resposta e falha.** Como no chat: o resumo final vira a mensagem da persona (até 90 000 bytes); falha, cancelamento,
   interrupção ou run sem resumo viram aviso de sistema "Persona: motivo".
9. **Papéis e atores (ADR-0021).** Iguais ao chat: todo membro lê; criar sala, escrever, pôr/tirar persona, mudar o modo,
   a moderadora e os limites, arquivar e reabrir exigem `member`; só ator humano escreve; não membro recebe 404. Run tokens
   não usam a API de conversas. Tirar uma persona não cancela a run dela em andamento (a resposta ainda chega, sem acordar
   ninguém) e descarta a fila dela; a sala mantém pelo menos 2 personas.
10. **Atividade e eventos.** Os verbos do chat (`conversation.created`, `.message`, `.replied`, `.archived`,
    `.reopened`) levam `kind`; a sala acrescenta `conversation.participant_added`, `.participant_removed` e
    `.room_updated`. `conversation.message` lista quem recebeu, quem ficou na fila e quem recusou; `conversation.replied`,
    o salto e quem foi acordado. Tudo na mesma transação da escrita.
11. **API.** As rotas de conversas: `POST /companies/{c}/conversations` com `kind=room`, `personas`, `moderator`, `mode`,
    `max_hops`, `max_runs_per_message`; `GET …?kind=direct|room` (com `persona`, a sala filtra por participante ativo);
    `POST …/{id}/participants`, `DELETE …/{id}/participants/{persona}`, `PATCH …/{id}/room`. `Conversation` ganha `kind` e
    `room` (configuração efetiva e participantes com a run aberta e a fila de cada um); `ConversationMessage` ganha
    `persona_id`/`persona_name`, `reply_to_id`, `mentions` e `deliveries` (a run de cada persona, ausente enquanto espera).
    Numa sala, `active_run_id` da conversa é a primeira run aberta e `pending_messages` soma as entregas na fila.
12. **CLI.** `hanko room create --persona a,b,c [--moderator] [--mode] [--max-hops] [--max-runs] [--message] "título"`,
    `hanko room say <id> "@a mensagem"` (espera a sala ficar ociosa e imprime as respostas com o nome de cada persona; sai
    com 1 se nenhuma respondeu), `hanko room list|show|add|remove|set`. `hanko chat list` passa a listar só chats diretos.
13. **UI.** `/c/{empresa}/sala`: lista com abas Abertas/Arquivadas/Todas e "Nova sala" (título, personas em
    multi-seleção com glifo, moderadora, modo, primeira mensagem); estado vazio com atalhos ("Planejar a semana", ...)
    que preenchem a primeira mensagem pela URL. `/sala/{id}`: ~2/3 conversa e ~1/3 **Feed do time** (a atividade da
    empresa, a mesma consulta da página Atividade, polling htmx de 10 s; abaixo da conversa e recolhível em telas
    estreitas). Cabeçalho com as personas (glifo, moderadora, indisponível, tirar), modo e custo; bolhas humano à direita,
    persona à esquerda com glifo e nome, sistema neutro; "X está pensando…" por persona com run ativa e polling de 2 s só
    enquanto houver uma. Composer com "@ para chamar uma persona" e os slugs (um clique insere a citação) e rascunho por
    sala no `sessionStorage` (JS opcional, no fim de `hanko.js`). `/chat` filtra `kind=direct`; `/chat/{id}` de uma sala
    leva à sala; a página da persona mostra "Salas em que participa".

## Consequências
- Uma sala gasta como várias conversas: cada resposta é uma run da persona, no orçamento dela e da empresa e na trava
  semanal. Com o padrão (2 saltos, teto = participantes) uma mensagem humana numa sala de 3 personas abre no máximo 3 runs.
  Uma run que recolheu entregas de duas cadeias conta nas duas (o freio erra para o lado de gastar menos).
- Uma persona pode estar rodando uma task, um chat e várias salas ao mesmo tempo, cada run com o próprio orçamento e a
  mesma trava semanal.
- Export/import (ADR-0022): salas, participantes e mensagens são estado de operação, como as conversas do chat, e não
  viajam; `hanko.company.v1` não muda.
- A moderadora fica no JSON da configuração, sem chave estrangeira: uma persona apagada some dos participantes (cascata)
  e a moderadora efetiva cai para a primeira participante.
- As mensagens de sistema da sala (recusas, limites, entrada e saída) ficam gravadas em pt-BR, como as do chat.
- Sem MCP novo: a persona "fala" na sala pela resposta da run e chama outra pela citação; não há tool para postar na sala
  fora de uma run, de propósito (só humano escreve).
- Fora desta versão: menções a humanos, anexos, busca dentro da sala, sessões compartilhadas entre personas e um tópico SSE
  por sala (o polling não muda o modelo nem a API).
