### Cofre: tela para exportar e importar o cofre cifrado (.hkv) (fatia 7, #62)

- Configurações › Segredos › Cofre cifrado (.hkv) (só admin): exporta o cofre com uma senha do arquivo (mínimo de 12 caracteres) e baixa o `.hkv`; importa um `.hkv` de outra instância com a senha do arquivo. As duas operações pedem a senha da conta quando o servidor exige reautenticação recente.
- Importar grava tudo ou nada: senha errada ou arquivo alterado não grava nada. Segredo que já existe não é trocado.
- Nenhuma senha volta à página, e o arquivo sai só na resposta do download.
- English summary: a new Settings › Secrets › Encrypted vault (.hkv) page (admins only) exports the vault as a downloadable `.hkv` file under a file password (at least 12 characters) and imports a `.hkv` from another instance with that password. Both ask for the account password when the server requires recent re-authentication. Imports are all-or-nothing, a wrong password writes nothing, and no password is echoed back.
