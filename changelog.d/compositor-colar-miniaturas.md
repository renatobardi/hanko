### Compositor: colar anexo, miniaturas, Enter envia, caixa fixa e PDF grande (ADR-0066, adendo)
- Colar imagem ou arquivo (Ctrl/Cmd+V) no chat, na sala e nos comentários de task anexa e mostra a miniatura; print sem nome ganha `colado-AAAA-MM-DD-HHMM.png`.
- Anexos aparecem como miniaturas (imagem pequena é a própria miniatura; o resto, ícone e extensão), com nome e tamanho ao passar o mouse; a prévia antes de enviar tem um X para remover cada arquivo. O seletor nativo "Choose Files" some.
- Enter envia, Shift+Enter quebra a linha (no celular o Enter quebra a linha); vale também para a primeira mensagem de uma conversa nova.
- A caixa de mensagem fica fixa embaixo da tela por padrão, com um alfinete para soltá-la (a escolha fica no navegador), e setas flutuantes levam ao início e ao fim da conversa; a conversa abre no fim e acompanha mensagem nova só se você estiver perto do fim.
- PDF grande (acima de 15 MiB ou 100 páginas): o heartbeat extrai o texto e divide em partes de 20 páginas antes da run, e o briefing manda ler o texto primeiro. `poppler-utils` e `qpdf` entram na imagem do container.
- English summary: paste-to-attach, attachment thumbnails, Enter-to-send, a pinned composer with start/end arrows, and large PDFs extracted to text and split into 20-page parts before a run.
