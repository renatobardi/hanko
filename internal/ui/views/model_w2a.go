package views

// BackupRowVM é uma linha do histórico de backups.
type BackupRowVM struct {
	When     string
	Kind     string // rótulo traduzido
	Status   string // running | ok | failed
	StatusTx string // rótulo traduzido
	File     string
	Size     string
	Duration string
	Label    string
	Error    string
	Blobs    int
	Actor    string
}

// BackupsVM é a página Configurações › Backups.
type BackupsVM struct {
	Sections  []TabLink
	Notice    string
	Error     string
	Forbidden bool // só o dono da instalação
	// Available: o servidor sabe fazer backups (diretório e pg_dump); sem isso a tela só explica.
	Available bool
	Running   bool // há um backup em andamento (o botão fica desligado e a tela se atualiza)
	Dir       string

	Enabled  bool
	Cron     string
	Timezone string
	Next     string
	Last     string

	ActionRun      string
	ActionSchedule string
	Rows           []BackupRowVM
}
