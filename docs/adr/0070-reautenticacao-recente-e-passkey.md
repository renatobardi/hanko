# ADR-0070 — Reautenticação recente e passkey para ações sensíveis do cofre
Data: 2026-10-24 · Status: aceita (2026-10-24) · Issue: #54 (decisão 10), contrato em #55, entrega em #57

## Contexto
O Hanko autentica por sessão em cookie, nos providers `local` (e-mail e senha com argon2id) e `firebase` (ADR-0007,
ADR-0014), e não tem segundo fator: não há TOTP, WebAuthn nem passkey em `internal/auth`. Hoje, um cookie de sessão
roubado já revela, rotaciona e apaga segredos.

O cofre (ADR-0068) e o envio (ADR-0069) aumentam o que uma sessão vale: histórico de valores, arquivos, links públicos
e concessão de coleções a personas. A decisão 10 de #54 foi proteger **essas ações** com uma prova recente de
identidade, sem mudar o login de todo mundo (2FA no login ficou fora).

## Decisão
1. **Ações sensíveis** (exigem prova recente): revelar campo ou versão do histórico, ver histórico, restaurar versão,
   criar envio, conceder ou revogar coleção, apagar segredo ou campo, importar `.env`, exportar e importar o cofre
   cifrado, cadastrar ou remover passkey.
2. **Janela.** A prova vale por `secrets.reauth_minutes` (instance setting, ADR-0050 §C; padrão 5, de 1 a 60) a partir
   do instante em que foi feita. A sessão guarda `reauth_at` e `reauth_method` (migration 00203). Sem prova válida, a
   API responde **401** com o código `reauth_required` e a janela exigida; a UI abre o pedido de prova e repete a ação.
3. **Provas aceitas.**
   - **Passkey (WebAuthn)**, em qualquer provider, quando o usuário cadastrou uma. Verificação de usuário (`UV`)
     obrigatória, desafio de uso único com validade de 2 minutos (`webauthn_challenges`), contador de assinatura
     conferido.
   - **Senha**, no provider `local` (o mesmo argon2id do login, com o limitador de tentativas do login).
   - **Token do Firebase com `auth_time` recente**, no provider `firebase`: a UI pede um novo login ao SDK (Google ou
     e-mail e senha), e o servidor aceita o ID token se `auth_time` estiver dentro da janela e o `uid` for o da sessão.
4. **Passkeys.** Cada usuário cadastra até 10 (`user_passkeys`: credencial, chave pública COSE, contador, AAGUID,
   transports, nome, últimos usos). Cadastrar e remover passkey também exigem prova recente (senha, Firebase ou outra
   passkey). Nesta entrega a passkey serve só para reautenticar; login por passkey fica para outro ADR.
5. **Quem não passa por reautenticação.**
   - **Persona** (run token): nunca chega a essas rotas de humano; as tools de persona (`hanko_secret_get`,
     `hanko_send_create`) são controladas pela política de tools (ADR-0053), com padrão `ask`.
   - **API key de humano** (CLI, automação): não há como reautenticar de forma interativa. A ação sensível
     é aceita só com chave de escopo `full` (ADR-0057; a de escopo `read` é recusada com 403), e o fato registra que a
     ação veio por API key, com o id da chave (aceito pelo Bardi em 2026-10-24, PR #63). Recusar as ações sensíveis por
     API key foi descartado: impediria `hanko secret export` e `import-env` na CLI.
   - **Modo trusted** (ADR-0017): sem identidade, sem reautenticação; já não há escopo de usuário nesse modo.
6. **Fatos.** `auth.reauthenticated` (método, sem segredo), `auth.passkey_added|passkey_removed`. Falha de prova entra
   no limitador de login e no log, sem o valor tentado.

## Dependência nova
WebAuthn exige validar atestação, CBOR/COSE e assinaturas de vários algoritmos. Escrever isso do zero é a parte mais
arriscada desta entrega. Decisão (aceita pelo Bardi em 2026-10-24, PR #63): usar `github.com/go-webauthn/webauthn` (licença BSD-3,
sem cgo), usado só em `internal/auth/passkey`, com atestação `none` (o Hanko não precisa provar o modelo do
autenticador). A regra do ADR-0011 (stdlib primeiro) cede aqui porque a alternativa é uma implementação própria de
criptografia.

## Alternativas descartadas
- **2FA no login para todos.** Protege tudo, mas muda o login de todos os usuários; ficou para uma issue separada (as
  duas convivem).
- **TOTP como prova de reautenticação.** Exige guardar uma semente por usuário e não resiste a phishing; a passkey
  resolve os dois providers com o mesmo mecanismo.
- **Janela por sessão inteira** (reautenticar uma vez por login). Um cookie roubado logo depois da prova abriria tudo.

## Consequências
- A UI ganha a tela de passkeys no perfil e o diálogo de reautenticação reaproveitável.
- Os testes de integração ganham um autenticador WebAuthn falso (chave gerada no teste) para cobrir cadastro e prova.
- Ação sensível feita depois da janela pede prova de novo. O custo para quem usa o cofre o dia todo é uma prova a cada
  5 minutos de uso sensível, ajustável pelo dono da instalação.
