### Cofre: campo arquivo pela tela

- "Novo campo" do item aceita o tipo `file`: a tela envia o arquivo (até 1 MiB, como `hanko secret field add --kind file`), o campo é sempre secreto e o conteúdo nunca volta à página; a lista mostra só o nome e o tamanho.
- "Trocar arquivo" troca o conteúdo pelo arquivo novo. O arquivo anterior vai para o histórico (ADR-0068 §3.1), a versão do campo sobe, e restaurar a versão anterior põe o arquivo de volta como versão nova (§3.3). "Apagar campo" apaga o arquivo do campo e o das versões anteriores.

- English summary: the secret's "New field" form accepts the `file` kind: the screen uploads the file (up to 1 MiB, like `hanko secret field add --kind file`), the field is always secret and the content never comes back to the page; the list shows only the name and size. "Replace file" replaces the content with the new file: the previous file goes to the history (ADR-0068 §3.1), the field's version goes up, and restoring a previous version puts its file back as a new version (§3.3). "Delete field" deletes the field's file and the files of its previous versions.
