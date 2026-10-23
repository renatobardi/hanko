package i18n

// enM8 holds the English texts of M8 (ADR-0052): instruction revisions, attachments, server-side drafts and the
// chat's /new. init merges them into the same catalog as the rest (en.go). Same rules: the key is the pt-BR text, and a
// key that another file already translates is not repeated here.
var enM8 = map[string]string{
	// web UI: instructions page (revisions, diff, restore)
	"revisão %d":                                      "revision %d",
	"revisão %d · histórico e edição":                 "revision %d · history and editing",
	"Voltar à persona":                                "Back to the persona",
	"Editar instruções":                               "Edit instructions",
	"Motivo da mudança":                               "Reason for the change",
	"Por que mudar (opcional)":                        "Why change it (optional)",
	"Salvar nova revisão":                             "Save as a new revision",
	"Restaurar esta revisão":                          "Restore this revision",
	"aplicada por %s":                                 "applied by %s",
	"texto anterior às revisões":                      "text from before revisions",
	"contratação":                                     "hire",
	"edição":                                          "edit",
	"restauração":                                     "restore",
	"importação":                                      "import",
	"proposta da persona aprovada":                    "approved persona proposal",
	"revisão inválida":                                "invalid revision",
	"Proposta da persona esperando decisão":           "Persona proposal waiting for a decision",
	"Instruções salvas: uma revisão nova foi criada.": "Instructions saved: a new revision was created.",
	"O texto não mudou: nenhuma revisão nova.":        "The text did not change: no new revision.",
	"Revisão restaurada: uma revisão nova foi criada com o texto dela.":                             "Revision restored: a new revision was created with its text.",
	"Restaurar esta revisão? Uma revisão nova é criada com o texto dela.":                           "Restore this revision? A new revision is created with its text.",
	"Cada mudança vira uma revisão imutável, com o motivo; a próxima execução já usa o texto novo.": "Every change becomes an immutable revision, with the reason; the next run already uses the new text.",

	// web UI: attachments, drafts, /new
	"Anexos":                      "Attachments",
	"Anexar arquivos":             "Attach files",
	"Arquivos a anexar":           "Files to attach",
	"Nenhum anexo ainda.":         "No attachments yet.",
	"Apagar este anexo?":          "Delete this attachment?",
	"Apagar anexo":                "Delete attachment",
	"apagar":                      "delete",
	"escolha ao menos um arquivo": "choose at least one file",
	"Enviei %d anexo(s).":         "I sent %d attachment(s).",
	"Até %s por arquivo, %d arquivos e %s por envio.": "Up to %s per file, %d files and %s per upload.",
	"no máximo %d arquivos por envio":                 "at most %d files per upload",
	"Rascunho salvo às %s":                            "Draft saved at %s",
	"Rascunho não salvo":                              "Draft not saved",
	"Contexto reiniciado":                             "Context restarted",

	// CLI: hanko persona history|restore, persona update --instructions
	"<persona> <revisão>": "<persona> <revision>",
	"Lista as revisões das instruções da persona (ou mostra uma com --show e compara com --against)": "Lists the persona's instruction revisions (or shows one with --show and compares with --against)",
	"Restaura o texto de uma revisão das instruções (cria uma revisão nova)":                         "Restores the text of an instruction revision (creates a new revision)",
	"mostra esta revisão, com o texto":                                  "shows this revision, with its text",
	"com --show, mostra o diff desta revisão para a pedida":             "with --show, shows the diff from this revision to the requested one",
	"motivo (fica na revisão)":                                          "reason (kept in the revision)",
	"revisão inválida: use um número a partir de 1":                     "invalid revision: use a number from 1",
	"Diff da revisão %d para a %d (+%d -%d):":                           "Diff from revision %d to %d (+%d -%d):",
	"Instruções de %s restauradas da revisão %d: agora na revisão %d\n": "Instructions of %s restored from revision %d: now at revision %d\n",
	"REVISÃO":      "REVISION",
	"APLICADA POR": "APPLIED BY",
	"TEXTO":        "TEXT",
	"Aplicada por": "Applied by",
	"novas instruções da persona (vira uma revisão)":    "new persona instructions (becomes a revision)",
	"arquivo com as novas instruções":                   "file with the new instructions",
	"motivo da mudança de instruções (fica na revisão)": "reason for the instruction change (kept in the revision)",
	"--instructions e --instructions-file não combinam": "--instructions and --instructions-file do not go together",

	// CLI: hanko attach, hanko attachment
	"<arquivo|->...": "<file|->...",
	"Anexa arquivos a uma task (ou comentário) ou a uma conversa do chat": "Attaches files to a task (or comment) or to a chat conversation",
	"Anexa um ou mais arquivos à task (--task) ou à conversa (--conversation). Com --comment o anexo pertence a um comentário da task; com --message, a uma mensagem da conversa. Um nome repetido na mesma task ganha sufixo (nome (2).ext).": "Attaches one or more files to the task (--task) or conversation (--conversation). With --comment the attachment belongs to a task comment; with --message, to a conversation message. A repeated name on the same task gets a suffix (name (2).ext).",
	"informe --task OU --conversation":                           "give --task OR --conversation",
	"--comment é da task e --message é da conversa":              "--comment is for a task and --message is for a conversation",
	"--name só vale com um arquivo":                              "--name only works with a single file",
	"o arquivo %s passa de %d bytes":                             "the file %s is over %d bytes",
	"Anexo %s anexado (%s, %s)\n":                                "Attachment %s attached (%s, %s)",
	"Anexo %s apagado.\n":                                        "Attachment %s deleted.",
	"task a que o anexo pertence (id ou chave)":                  "task the attachment belongs to (id or key)",
	"conversa do chat a que o anexo pertence (id)":               "chat conversation the attachment belongs to (id)",
	"comentário da task a que o anexo pertence (id)":             "task comment the attachment belongs to (id)",
	"mensagem da conversa a que o anexo pertence (id)":           "conversation message the attachment belongs to (id)",
	"nome do anexo (padrão: o nome do arquivo)":                  "attachment name (default: the file name)",
	"Consulta e apaga anexos":                                    "Lists, downloads and deletes attachments",
	"Lista anexos de uma task, comentário, conversa ou mensagem": "Lists attachments of a task, comment, conversation or message",
	"informe --task, --conversation, --comment ou --message":     "give --task, --conversation, --comment or --message",
	"Baixa o conteúdo de um anexo (saída padrão ou -o arquivo)":  "Downloads an attachment's content (standard output or -o file)",
	"Apaga um anexo (admin)":                                     "Deletes an attachment (admin)",
	"comentário (id)":                                            "comment (id)",
	"conversa (id)":                                              "conversation (id)",
	"mensagem (id)":                                              "message (id)",

	// CLI: hanko chat /new
	"Contexto reiniciado. A conversa %s recomeça na próxima mensagem.": "Context restarted. Conversation %s starts over with the next message.",
}

func init() {
	for k, v := range enM8 {
		if _, dup := en[k]; dup {
			panic("i18n: duplicate catalog key " + k)
		}
		en[k] = v
	}
}
