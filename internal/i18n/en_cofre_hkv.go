package i18n

// enCofreHkv traz os textos em inglês do cofre cifrado (.hkv, ADR-0068 §8.2 e §8.3, #62): exportar e importar o arquivo
// na tela de Configurações › Segredos. Mesma regra de en_portabilidade.go: a chave é o texto pt-BR, e chave que outro
// arquivo já traduz não se repete. Lista de pares, não mapa, para o gofmt não alinhar as colunas.
var enCofreHkv = [][2]string{
	{"Cofre cifrado (.hkv)", "Encrypted vault (.hkv)"},
	{"Arquivo do cofre importado.", "Vault file imported."},
	{"A senha do arquivo precisa de pelo menos %d caracteres.", "The file password needs at least %d characters."},
	{"O arquivo passa de %s.", "The file is over %s."},
	{"Envio inválido.", "Invalid upload."},
	{"Escolha um arquivo .hkv.", "Choose a .hkv file."},
	{"Não foi possível ler o arquivo.", "Could not read the file."},
	{"O servidor pede uma prova recente de identidade: informe a senha da conta e envie de novo.", "The server asks for recent proof of identity: enter the account password and send again."},
	{"Senha errada ou arquivo alterado: nada foi gravado.", "Wrong file password or altered file: nothing was written."},
	{"Arquivo .hkv inválido: nada foi gravado.", "Invalid .hkv file: nothing was written."},
	{"Exporte o cofre com uma senha para importar em outra instância, ou importe um arquivo exportado. Os valores ficam dentro do arquivo cifrado.", "Export the vault with a password to import it into another instance, or import an exported file. The values stay inside the encrypted file."},
	{"Exportar o cofre", "Export the vault"},
	{"Gera um arquivo .hkv com segredos, campos, coleções e concessões. Ele é cifrado com a senha abaixo; nenhum valor fica em claro.", "Creates a .hkv file with secrets, fields, collections and grants. It is encrypted with the password below; no value is left in clear text."},
	{"Senha do arquivo (mínimo de 12 caracteres)", "File password (at least 12 characters)"},
	{"Senha da conta, para confirmar", "Account password, to confirm"},
	{"Baixar o arquivo .hkv", "Download the .hkv file"},
	{"Importar um arquivo .hkv", "Import a .hkv file"},
	{"Grava tudo o que o arquivo leva, ou nada. Segredo que já existe aqui não é trocado. Senha errada não grava nada.", "Writes everything the file holds, or nothing. A secret that already exists here is not replaced. A wrong password writes nothing."},
	{"Arquivo .hkv", ".hkv file"},
	{"Senha do arquivo", "File password"},
	{"Importar o arquivo", "Import the file"},
}

func init() {
	for _, kv := range enCofreHkv {
		if _, dup := en[kv[0]]; dup {
			panic("i18n: chave duplicada em en_cofre_hkv.go: " + kv[0])
		}
		en[kv[0]] = kv[1]
	}
}
