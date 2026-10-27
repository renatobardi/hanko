### Cofre: campo arquivo pela tela

- "Novo campo" do item aceita o tipo `file`: a tela envia o arquivo (até 1 MiB, como `hanko secret field add --kind file`), o campo é sempre secreto e o conteúdo nunca volta à página; a lista mostra só o nome e o tamanho. Campo arquivo não se troca (ADR-0068 §5.1): a tela ganhou "Apagar campo" para apagar e criar de novo com o arquivo novo.

- English summary: the secret's "New field" form accepts the `file` kind: the screen uploads the file (up to 1 MiB, like `hanko secret field add --kind file`), the field is always secret and the content never comes back to the page; the list shows only the name and size. A file field cannot be replaced (ADR-0068 §5.1), so the screen gained "Delete field" to delete and create again with the new file.
