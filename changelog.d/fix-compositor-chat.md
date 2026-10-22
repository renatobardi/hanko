### Correção — compositores de chat, sala e comentário da task
- O texto digitado sumia ~1 s depois da última tecla: o autosave do rascunho disparava a limpeza do formulário (o evento `htmx:afterRequest` da caixa de texto subia até o `<form>`). Agora só o envio do próprio formulário, com resposta 2xx, limpa o texto e os arquivos; um erro mostrado no aviso não apaga mais o que foi escrito.
- Anexos escolhidos ou arrastados eram descartados pelo mesmo motivo antes do envio; agora chegam com a mensagem.
- O autosave cede ao envio (`hx-sync="closest form:abort"`): não regrava o rascunho que o envio acabou de apagar.
- A lista da conversa não se atualiza durante um envio, e uma atualização em andamento é cancelada, para não trocar a mensagem nova por uma lista velha. A conversa da task passa a se atualizar sozinha enquanto a persona trabalha ou acabou de ser chamada (`GET /c/{company}/tasks/{task}/conversa`).
- Erros sem fragmento (5xx de proxy, falha de rede) aparecem no aviso da página; o botão de envio fica desabilitado durante o envio.
- English summary: chat/room/task composers no longer wipe the text and the chosen files when the draft autosaves; sends can't be hidden by an in-flight poll, the task thread refreshes while the persona works, and network/server errors show in the flash.
