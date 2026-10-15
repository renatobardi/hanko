# ADR-0021 — Multi-humano: papéis por empresa e convites
Data: 2026-10-15 · Status: aceita

## Contexto
O ADR-0007 deixou o multi-humano para depois: hoje todo usuário autenticado faz tudo em todas as empresas (`users.role`
só registra quem é o dono da instalação). Para ter mais de uma pessoa operando o mesmo Hanko é preciso separar quem
vê, quem opera e quem administra, por empresa, sem mudar o comportamento de quem já usa.

## Decisão
1. **Papel por empresa.** Tabela `company_members (company_id, user_id, role)` com `owner | admin | member | viewer`
   (migration `00011_membros.sql`). O backfill torna todo usuário existente membro de TODAS as empresas existentes, com
   o papel que ele já tinha na instalação (`users.role`: owner→owner, admin→admin, member→member), o que preserva o
   comportamento atual. Quem cria uma empresa vira owner dela. O dono da instalação (`users.role = owner`) é owner de
   todas as empresas, tenha ou não linha em `company_members`.
2. **Matriz de autorização** (aplicada no service; API, UI, CLI e MCP usam o mesmo código):

   | Ação | viewer | member | admin | owner |
   |---|:-:|:-:|:-:|:-:|
   | Ler tudo da empresa (tasks, execuções, gates, custos, atividade, organograma, membros) | sim | sim | sim | sim |
   | Criar/editar tasks, comentar, acordar, cancelar execução, criar projeto | não | sim | sim | sim |
   | Decidir gates `approval` e `task_review` | não | sim | sim | sim |
   | Decidir gate `hire` (contratação) | não | não | sim | sim |
   | Contratar/editar personas, orçamentos (empresa e persona), assinaturas, fluxos, workspaces | não | não | sim | sim |
   | Ver convites abertos; convidar, revogar convite, mudar papel e remover membros `member`/`viewer` | não | não | sim | sim |
   | Conceder/mudar/remover `admin` e `owner` | não | não | não | sim |

   Qualquer membro pode sair da empresa (remover a si mesmo). Criar empresa é de quem tem `users.role` owner ou admin
   (convidados entram como `member` na instalação). Excluir empresa não existe hoje e, quando existir, será só do owner.
3. **Sem associação → 404, não 403.** Um usuário que não é membro recebe `ErrNotFound` em qualquer acesso por empresa
   (id ou slug) e não a vê em `ListCompanies`; papel insuficiente de quem é membro é `ErrForbidden` (403, que vale como
   `ErrNotAllowed`). Assim a existência de uma empresa não vaza.
4. **Quem chama.** A barreira só restringe quando há `auth.Identity` no contexto (sessão ou API key de um usuário). Sem
   identidade (modo trusted, heartbeat, persona com run token) o service não aplica papéis: a persona continua limitada à
   empresa do token pela API e pelo MCP, e `DecideGate` continua exclusivo de humano.
5. **Nunca sem owner.** Rebaixar ou remover o último owner é `ErrConflict`; as mudanças de membros serializam por trava
   na linha da empresa, então duas saídas simultâneas não deixam a empresa vazia.
6. **Convites.** `company_invites` guarda e-mail (minúsculas), papel, `token_hash` (SHA-256 do token de 32 bytes
   aleatórios), validade de 7 dias, e quem convidou. O token nunca é gravado em claro nem entra em log ou atividade; só
   a resposta da criação o mostra, como link `/convite/{token}` para o admin copiar. Não há envio de e-mail (sem SMTP):
   para reemitir, convida-se de novo (o convite aberto anterior daquele e-mail é revogado). Há no máximo um convite aberto
   por empresa e e-mail; convidar quem já é membro é `ErrConflict`. Admin só concede `member`/`viewer`; `owner` e `admin`
   exigem owner.
7. **Aceite.** A página `/convite/{token}` é pública (não passa pelo login) mas só aceita com credencial:
   - `local`: sem conta com aquele e-mail, cria a conta (nome e senha) já como `member` da instalação e aceita; com conta,
     pede a senha; logado com o e-mail do convite, basta confirmar;
   - `firebase`: login pela mesma tela de entrada; o e-mail do ID token (já verificado) tem de ser o do convite;
   - e-mail diferente, expirado, revogado, já usado ou token desconhecido são a mesma resposta (`ErrInviteInvalid`, 404),
     sem dizer qual. O consumo é atômico (uso único); a comparação do hash é em tempo constante;
   - o aceite reaproveita o limitador de login (por IP, e por IP+e-mail na senha); token inválido conta como falha;
   - a página responde `Referrer-Policy: no-referrer`.
   No `firebase` com listas de admissão, o convite vale como admissão: quem aceitou fica `users.invited = true` e continua
   admitido fora das listas. Usuários que existiam antes ficam `invited = false`, então a regra do ADR-0018 (sair da lista
   revoga) não muda para eles.
8. **Auditoria.** `member.invited`, `invite.revoked`, `member.joined`, `member.role_changed` e `member.removed` geram
   `activity_event` na mesma transação (sem o token).
9. **Superfícies.** API: `GET /companies/{c}/members`, `GET|POST /companies/{c}/invites`, `DELETE /companies/{c}/invites/{id}`,
   `PATCH|DELETE /companies/{c}/members/{id|e-mail}`; run tokens não acessam esses endpoints. UI: seção "Membros" em
   Configurações da empresa (modo authenticated). CLI: `hanko member list|invite|role|remove|revoke`.

## Consequências
Usuários novos só veem as empresas às quais foram convidados. A UI esconde (ou desabilita, com um aviso discreto "somente leitura" / "requer admin")
os controles que o papel não tem e mostra "você: <papel>" na barra lateral; é só UX, o service segue recusando com 403 e
uma mensagem clara se alguém forjar o POST. O
link do convite não pode ser recuperado depois de criado (só o hash existe). Remover alguém da empresa não encerra a
sessão global dele: ele apenas perde o acesso àquela empresa. O estado do limitador continua por processo (ADR-0018).

**Provider local: o link vale como prova de identidade.** Quem abre o link cria a conta com o e-mail do convite
sem confirmar a posse da caixa de entrada. Mitigações: (a) convidar um e-mail que ainda não tem conta só é permitido
a owner da empresa (admin só convida contas existentes; no provider firebase o ID token prova o e-mail e a regra não
vale); (b) o `member.joined` registra `invited_by`, então a trilha mostra quem abriu a porta; (c) `users.invited` só
marca conta criada pelo convite e zera quando a pessoa fica sem nenhuma empresa; (d) o dono da instalação
(`users.role = owner`) tem `hanko auth user list|reset-password|remove` para listar contas, redefinir senha (senha
temporária impressa uma única vez, ou `HANKO_USER_PASSWORD`; as sessões da conta caem) e remover contas, sem mexer no
banco. O service reconfere o papel no banco a cada chamada; o dono da instalação e o único owner de uma empresa não
são removidos. Quem não participa de nenhuma empresa vê a página "nenhuma empresa" em vez do onboarding.
