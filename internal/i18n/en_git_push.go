package i18n

// enGitPush holds the English texts of the push of the task's worktree branch (ADR-0091): the Git tab button and
// notices, and the CLI command.
var enGitPush = map[string]string{
	"Branch enviada ao repositório remoto.":                          "Branch pushed to the remote repository.",
	"O remoto já tinha o último commit da branch; nada foi enviado.": "The remote already had the branch's latest commit; nothing was pushed.",
	"Enviar branch": "Push branch",
	"Envia a branch ao repositório remoto com a credencial do workspace; alterações soltas viram um commit antes.": "Pushes the branch to the remote repository with the workspace credential; loose changes become a commit first.",
	"Envio ao remoto:": "Pushed to remote:",
	"nunca":            "never",
	"em dia":           "up to date",
	"atrasado":         "behind",
	"Envia a branch do worktree da task ao repositório remoto (membro); alterações soltas viram um commit antes": "Pushes the task's worktree branch to the remote repository (member); loose changes become a commit first",
	"Branch %s enviada: %s (%d commits desde a base)\n":                                                          "Branch %s pushed: %s (%d commits since the base)\n",
	"Branch %s já estava no remoto em %s (%d commits desde a base)\n":                                            "Branch %s was already on the remote at %s (%d commits since the base)\n",
	"(alterações soltas viraram um commit antes do envio)":                                                       "(loose changes became a commit before the push)",
}

func init() {
	for k, v := range enGitPush {
		if _, dup := en[k]; !dup {
			en[k] = v
		}
	}
}
