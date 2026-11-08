# ADR-0091 — Push da branch do worktree: o Hanko envia a branch da task ao remoto, por ação explícita
Data: 2026-11-08 · Status: aceita (Bardi, 2026-11-08: "segue com A"; item 6 do diagnóstico da produção) · Revisa: ADR-0054 §A.7 e §A.12 · Usa: ADR-0050, ADR-0053, ADR-0067

## Contexto
O ADR-0054 pôs a persona a trabalhar num worktree do espelho, commitando na branch da task, e decidiu que "o Hanko não faz
push": a branch vive no espelho e o humano vê diff, log e arquivos na aba Git. Na produção da `oute` isso travou o fluxo
inteiro de entrega: a persona termina o trabalho e não tem como abrir um pull request, porque a branch nunca chega ao
remoto. Dentro da run não há credencial (a do workspace só entra na busca do espelho, por cabeçalho fora do argv e do
disco) nem `gh`; o aviso do Delivery Manager foi literal: "fornecer um checkout com permissão de escrita ou autorizar o gh".

Alternativas vistas:
- **B. Só documentar o caminho via integração do repositório** (`create_branch`, `push_files`, `create_pull_request`). Sem código,
  mas `push_files` reenvia conteúdo pela API em vez dos commits locais: perde autoria e histórico, não serve a binários nem a
  diffs grandes, e a branch remota diverge da branch do espelho, então a aba Git deixa de refletir o que foi aberto no PR.
  Essas tools nascem em quarentena (ADR-0067) e precisam ser liberadas uma a uma.
- **A. O Hanko faz o push, por ação explícita.** Escolhida.

## Decisão
1. **Quem envia é o Hanko, nunca a persona por conta própria.** `Service.PushTaskWorktree` roda `git push origin
   <branch>` a partir do **espelho** (a branch de trabalho mora em `refs/heads/*` dele), sem força, com a credencial do
   workspace resolvida na hora e passada como na busca (cabeçalho escopado ao host ou chave ssh temporária; fora do argv e
   do disco; valor removido dos erros). Um remoto que tenha commits que não estão aqui recusa o envio, e o erro diz isso.
   Depois do envio, `refs/remotes/origin/<branch>` passa a apontar para o commit enviado, como uma busca faria.
2. **Ação explícita, nunca no fim da run.** Três portas, todas registradas em `activity_event` (`task.worktree_pushed`,
   com branch, commit, quantidade de commits e host do remoto; nunca a credencial):
   - **Membro**: botão "Enviar branch" na aba Git da task, `POST /tasks/{task}/git/push` e `hanko task git push <task>`.
   - **Persona**: tool `hanko_git_push`, só com o token de uma run de task. Passa pela política de tools **como tool do
     Hanko** (ADR-0053, igual a `hanko_secret_get`): sem política a chamada **pergunta ao gate** (o padrão da empresa); o
     admin libera com `hanko tool-policy set allow --native hanko_git_push --persona <slug>` ou desliga com `off`. O
     briefing da run deixa de dizer só "não faça push" e passa a apontar a tool.
   - O prompt e a descrição da tool dizem o passo seguinte: com a branch no remoto, o PR se abre pela integração do
     repositório (`create_pull_request`), que já existe; o Hanko não precisa saber o que é um PR.
3. **Nada se perde.** Alterações soltas no worktree viram um commit de segurança antes do envio, como na limpeza
   (ADR-0054 §A.9). O envio vale com o worktree vivo, divergente ou já limpo (a branch segue no espelho); não vale em
   `provisioning` nem `failed`.
4. **Registro.** `task_worktrees.pushed_sha` e `pushed_at` (migration 00219) guardam o último commit enviado; a aba Git
   mostra "Envio ao remoto: nunca | em dia (sha) | atrasado (sha)" e a API devolve `pushed_sha`/`pushed_at` no worktree.
5. **Credencial com escrita.** A credencial do workspace passa a precisar de permissão de escrita no repositório para o
   envio funcionar (até aqui bastava leitura). Nada muda para quem só sincroniza: o push falha com o erro do remoto e o
   espelho segue em dia. O RUNBOOK diz isso onde a credencial é configurada.
6. **Fora deste ADR.** Abrir o PR pelo Hanko; push automático; push para outro remoto ou com outro nome de branch; force
   push; ambientes remotos (ADR-0054 §A.12 segue valendo: o git do worktree não funciona lá).

## Consequências
- ADR-0054 §A.7 ("O Hanko não faz push") e §A.12 ("push/PR fora do escopo") ficam revisados por este ADR; o resto segue.
- Superfícies novas: tool `hanko_git_push`, rota `POST /tasks/{task}/git/push`, comando `hanko task git push`, botão na aba
  Git. Migration 00219, aditiva.
- Testes: `TestPushTaskWorktree` (envio a um remoto `file://`, commit de segurança, "em dia", registro e evento) e
  `TestGitPushForRunFollowsToolPolicy` (sem política pergunta ao gate e não envia; com `allow` envia; com `off` recusa).
