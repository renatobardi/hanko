# ADR-0085 — Hanko Desktop: app de barra de menu, chave no Keychain e aprovação pelo Terminal
Data: 2026-10-25 · Status: aceita (Bardi, 2026-10-25) · Issue: #66 (v0.8) · Usa: ADR-0042, ADR-0076, ADR-0077, ADR-0080, ADR-0081, ADR-0082, ADR-0083

## Contexto
O Tray do oute é um app Swift de barra de menu no macOS, que lê o agent-studio (`tray/` em `845764f` do oute-agent):

- **Barra.** Mostra `"<pendentes> · <alertas>"`, e `"? · ?"` antes da primeira leitura
  (`tray/Sources/TrayCore/TraySnapshot.swift:168-174`).
- **Menu.** Tem 12 itens (`tray/Sources/OuteTray/TrayMenu.swift:39-146`): erro de config; "sem leitura há X"; pedidos
  (ver script, aprovar, recusar); decisões do swarm; avisos das rodadas; etapas; máquinas; custo de hoje; erros na
  última hora; alertas (3 no menu, o resto em submenu); abrir; atualizar; versão; sair.
- **Aprovar e recusar.** Valida o id por `^[0-9]{8}-[0-9]{6}-[a-z0-9-]+$` e abre o Terminal no `oute approve <id>`,
  local ou por `ssh -t <alias>`. O alias de cada host vem de `~/.oute/tray-hosts`
  (`tray/Sources/TrayCore/ApproveCommand.swift:5-13`; `tray/Sources/OuteTray/Terminal.swift:6-20`;
  `tray/Sources/TrayCore/TrayHosts.swift`).
- **Notificações nativas.** Avisam pedido, etapa e aviso; o aviso resolvido sai do centro de notificações
  (`tray/Sources/OuteTray/Notifications.swift`; `TrayModel.swift:64-76`). Hoje são o único aviso sem modelo que chega
  ao Bardi (`docker/swarm/acompanhamento.md:7@845764f`).
- **"Visto" em memória.** Só avisa item novo, nunca na primeira leitura (`tray/Sources/TrayCore/Seen*.swift`).
- **Config.** O token de leitura vem de `~/.oute/agent.env`, em texto puro. Só aceita `https`, faz polling a cada
  15 s, com timeout de 10 s, sem cookie, sem cache e sem redirecionamento (`tray/Sources/TrayCore/TrayConfig.swift:18-75`).
- **Instalação.** É `swift build` e `.app` em `~/Applications/OuteTray.app`, com assinatura sem identidade, o
  LaunchAgent `pro.oute.tray` e sync por commit (`scripts/oute:1789-1956@845764f`).
- **Testes.** São 16 arquivos em `tray/Tests/TrayCoreTests/`, mas o CI não roda `swift test`
  (`.github/workflows/pr.yml:29-30@845764f`).

Decisões que este ADR já recebe:
- da #66: a decisão 4 (produto do Hanko, no repo do Hanko, genérico; "aprovar" abre o Terminal) e a decisão 11
  (consumidores reescritos para a API do Hanko);
- do gate da #67 (`docs/research/2026-10-inventario-agent-studio.md` §9.1 ponto 6 e §9.2 blocos 2 e 5): a sakura do
  Hanko como ícone, uma chave `read` própria, notificações nativas, o "visto" local e o Web Push desligado por padrão
  na Oute.

O alias de ssh ficou para este ADR (ADR-0077). As escolhas abaixo são do Bardi, em 2026-10-25.

## Decisão
1. **Produto.** O Hanko Desktop é um app Swift de barra de menu no macOS, que fica em `desktop/` no repo do Hanko. O
   código do Tray é portado, e os testes vão junto. Ele é genérico: lê uma empresa do Hanko pela API, e o que só o
   oute tem aparece porque é dado da empresa Oute. O ícone é a sakura do Hanko (`internal/ui/static/favicon.svg`);
   na barra de menu aparece só o traço, como hoje.
2. **Barra e menu.** A barra mostra `"<pendentes> · <alertas>"`. O menu mantém os 12 itens de hoje, com os conceitos
   novos:
   - pedidos → aprovações externas (ADR-0081);
   - decisões do swarm → perguntas da rodada (ADR-0080 item 6);
   - etapas → etapas da rodada (ADR-0080);
   - máquinas → hosts externos (ADR-0077);
   - custo de hoje → modelo de custo do Hanko (ADR-0082);
   - alertas → alertas da empresa (ADR-0083).

   "Abrir" leva à tela do Hanko. A API própria do Desktop substitui o `GET /v1/tray`, sem camada de compatibilidade.
3. **Aprovar sempre pelo Terminal.** "Aprovar" e "recusar" abrem o Terminal no `oute approve <id>`, com o mesmo
   padrão de id de hoje. O Desktop nunca decide nada pelo Hanko, e o Hanko nunca roda nada no host.
4. **Alias de ssh é local.** Um arquivo do Desktop, por máquina, liga o nome do host externo ao comando (local, ou
   `ssh -t <alias>`). O Hanko não guarda nada disso. A instalação grava os valores padrão de hoje. Para um host sem
   linha no arquivo, "aprovar" mostra o comando para copiar.
5. **Chave no Keychain.** O Desktop usa a chave `read` própria (ADR-0076 item 10), guardada no Keychain do macOS e
   nunca em arquivo. O comando de configuração pede a chave uma vez, e também serve para trocar ou revogar. Ficam as
   regras de hoje: só `https`, polling de 15 s, timeout de 10 s, sem cookie, sem cache e sem redirecionamento.
6. **Notificações nativas.** O Desktop avisa aprovação externa, etapa da rodada e item de atenção, só quando o item é
   novo e nunca na primeira leitura. O aviso resolvido sai do centro de notificações. O "visto" do Desktop é local e
   não mexe na Caixa por pessoa (ADR-0042). O Web Push nasce desligado na empresa Oute, e cada pessoa pode ligá-lo.
7. **Instalação na máquina.** Um script do repo do Hanko faz a instalação:
   - compila na própria máquina e põe o app em `~/Applications/Hanko Desktop.app`, assinado sem identidade;
   - cria o LaunchAgent `pro.oute.hanko.desktop`;
   - configura o Keychain e o arquivo de hosts.

   Atualizar é rodar o script de novo com a tag nova. Antes de instalar, o script confere se o `OuteTray` está
   instalado e, se estiver, para e manda rodar `oute tray uninstall`.
8. **CI no macOS.** Todo PR que toca `desktop/` compila e roda `swift test` num runner macOS.

## Consequências
- O Hanko ganha um segundo produto, em outra linguagem, com CI próprio.
- Sem a chave no Keychain, o Desktop mostra o erro de config e não lê nada.
- Trocar de Mac exige rodar a configuração de novo: a chave e o arquivo de hosts não vão junto.
- As issues #448 (aprovar pelo menu) e #612 (tray como painel) do oute-agent são triadas na saída do oute-agent
  (bloco 7 do gate da #67).

## Alternativas descartadas
- **Alias de ssh no Hanko, como dado do host externo**: o Hanko guardaria um detalhe da máquina de quem aprova, e um
  Mac com outro alias quebraria.
- **Chave num arquivo `0600`**: a chave ficaria em texto puro no disco e nos backups.
- **`.app` pronto no release**: sem a assinatura da Apple, o app esbarra no Gatekeeper; com ela, é preciso uma conta
  paga de desenvolvedor.
