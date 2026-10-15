package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/artifactsrv"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/textdiff"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Artefatos (ADR-0028). Ler é de qualquer membro; enviar é de member; excluir é de admin. A barreira de papel é o
// service; a UI só esconde os controles. O conteúdo é de terceiros: a prévia escapa o texto, o markdown passa pelo
// renderizador que escapa HTML, imagens só dos tipos raster seguros, e o download sai por artifactsrv.

const (
	artifactPageSize   = 50
	artifactPreviewMax = 256 << 10 // bytes de texto lidos para a prévia
	diffRowsMax        = 3000      // linhas do diff desenhadas na tela
	uploadFormOverhead = 1 << 20   // folga do corpo multipart além do arquivo (campos e fronteiras)
)

var artifactNotices = map[string]string{
	"enviado":  i18n.N("Artefato enviado."),
	"excluido": i18n.N("Artefato excluído."),
}

// fmtBytes é um tamanho legível (binário): "812 B", "1,5 KiB", "5 MiB".
func fmtBytes(ctx context.Context, n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v, i := float64(n), -1
	for v >= unit && i < 3 {
		v /= unit
		i++
	}
	suffixes := [...]string{"KiB", "MiB", "GiB", "TiB"}
	suffix := suffixes[max(0, min(i, len(suffixes)-1))]
	s := strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)
	return decimalComma(ctx, s) + " " + suffix
}

func artifactKindLabel(ctx context.Context, mime string) string {
	switch domain.KindOf(mime) {
	case domain.KindMarkdown:
		return "Markdown"
	case domain.KindText:
		return i18n.T(ctx, "Texto")
	case domain.KindImage:
		return i18n.T(ctx, "Imagem")
	}
	return i18n.T(ctx, "Outro")
}

// artifactLimitMessage explica a recusa por tamanho ou por cota, com os números.
func artifactLimitMessage(ctx context.Context, e *service.ArtifactLimitError) string {
	if e.Quota {
		return i18n.Tf(ctx, "A cota de artefatos da empresa foi atingida: %s de %s em uso. Apague artefatos antigos ou peça a um admin para subir a cota.", fmtBytes(ctx, e.Used), fmtBytes(ctx, e.Limit))
	}
	return i18n.Tf(ctx, "O arquivo passa do limite de %s por arquivo.", fmtBytes(ctx, e.Limit))
}

func artifactMini(ctx context.Context, a service.ArtifactView, base string) views.ArtifactMini {
	return views.ArtifactMini{Name: a.Name, Href: base + "/artefatos/" + a.ID.String(), Version: a.Version, Versions: a.Versions,
		Size: fmtBytes(ctx, a.SizeBytes), Kind: artifactKindLabel(ctx, a.Mime)}
}

func artifactMinis(ctx context.Context, list []service.ArtifactView, base string) []views.ArtifactMini {
	out := make([]views.ArtifactMini, 0, len(list))
	for _, a := range list {
		out = append(out, artifactMini(ctx, a, base))
	}
	return out
}

// ---------- lista ----------

func (s *server) artifactKindOptions(ctx context.Context, sel string) []views.Option {
	return []views.Option{
		{Value: "", Label: i18n.T(ctx, "Todos os tipos"), Selected: sel == ""},
		{Value: string(domain.KindMarkdown), Label: "Markdown", Selected: sel == string(domain.KindMarkdown)},
		{Value: string(domain.KindText), Label: i18n.T(ctx, "Texto"), Selected: sel == string(domain.KindText)},
		{Value: string(domain.KindImage), Label: i18n.T(ctx, "Imagem"), Selected: sel == string(domain.KindImage)},
		{Value: string(domain.KindOther), Label: i18n.T(ctx, "Outro"), Selected: sel == string(domain.KindOther)},
	}
}

func (s *server) handleArtifacts(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	q := r.URL.Query()
	base := "/c/" + co.Slug
	query := strings.TrimSpace(q.Get("q"))
	projRef, personaRef := strings.TrimSpace(q.Get("projeto")), strings.TrimSpace(q.Get("persona"))
	taskRef, kind := strings.TrimSpace(q.Get("task")), strings.TrimSpace(q.Get("tipo"))
	runRef := strings.TrimSpace(q.Get("execucao"))
	page, _ := strconv.Atoi(q.Get("pagina"))
	if page < 1 || page > 1000 {
		page = 1
	}

	filter := service.ArtifactFilter{Query: query, Limit: artifactPageSize + 1, Offset: (page - 1) * artifactPageSize}
	if kind != "" {
		k := domain.ArtifactKind(kind)
		if !k.Valid() {
			s.fail(w, r, &co, domain.Invalid("tipo", i18n.T(ctx, "valor desconhecido")))
			return
		}
		filter.Kind = k
	}
	if projRef != "" {
		pv, err := s.be.GetProjectView(ctx, co.ID, projRef)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		filter.ProjectID = &pv.ID
	}
	if personaRef != "" {
		p, err := s.be.GetPersona(ctx, co.ID, personaRef)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		filter.PersonaID = &p.ID
	}
	if taskRef != "" {
		t, err := s.be.GetTask(ctx, co.ID, taskRef)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		filter.TaskID = &t.ID
	}
	if runRef != "" {
		id, err := domain.ParseID(runRef)
		if err != nil {
			s.fail(w, r, &co, domain.ErrNotFound)
			return
		}
		filter.RunID, filter.AllVersions = &id, true
	}
	list, err := s.be.ListArtifacts(ctx, co.ID, filter)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	usage, err := s.be.GetArtifactUsage(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	projects, err := s.be.ListProjects(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}

	active := query != "" || projRef != "" || personaRef != "" || taskRef != "" || kind != "" || runRef != ""
	vm := views.ArtifactsVM{
		Subtitle: i18n.T(ctx, "Documentos e arquivos produzidos pelas personas."),
		Notice:   i18n.T(ctx, artifactNotices[q.Get("ok")]),
		NewHref:  base + "/artefatos/novo",
		Chains:   strconv.Itoa(usage.Versions), Used: fmtBytes(ctx, usage.UsedBytes), Quota: fmtBytes(ctx, usage.QuotaBytes),
	}
	if usage.QuotaBytes > 0 {
		vm.UsedPct = int(math.Min(100, float64(usage.UsedBytes)*100/float64(usage.QuotaBytes)))
	}
	vm.Filters = views.ArtifactFilterVM{Action: base + "/artefatos", Query: query, Task: taskRef, Active: active, ClearHref: base + "/artefatos",
		ProjectOptions: []views.Option{{Value: "", Label: i18n.T(ctx, "Todos os projetos"), Selected: projRef == ""}},
		PersonaOptions: []views.Option{{Value: "", Label: i18n.T(ctx, "Todas as personas"), Selected: personaRef == ""}},
		KindOptions:    s.artifactKindOptions(ctx, kind)}
	for _, p := range projects {
		vm.Filters.ProjectOptions = append(vm.Filters.ProjectOptions, views.Option{Value: p.Slug, Label: p.Name, Selected: p.Slug == projRef})
	}
	for _, p := range personas {
		vm.Filters.PersonaOptions = append(vm.Filters.PersonaOptions, views.Option{Value: p.Slug, Label: p.Name, Selected: p.Slug == personaRef})
	}
	if len(list) > artifactPageSize {
		vm.More = true
		list = list[:artifactPageSize]
		next := cloneQuery(q)
		next.Set("pagina", strconv.Itoa(page+1))
		vm.MoreHref = base + "/artefatos?" + next.Encode()
	}
	vm.Empty = len(list) == 0 && !active && page == 1
	loc := loadLoc(co.Timezone)
	now := s.now()
	for _, a := range list {
		row := views.ArtifactRow{Name: a.Name, Href: base + "/artefatos/" + a.ID.String(), Kind: artifactKindLabel(ctx, a.Mime), Mime: a.Mime,
			Size: fmtBytes(ctx, a.SizeBytes), Version: a.Version, Versions: a.Versions, When: agoCtx(ctx, a.CreatedAt, now, loc)}
		if a.TaskKey != "" {
			row.TaskKey, row.TaskHref, row.TaskTitle = a.TaskKey, base+"/tasks/"+a.TaskKey, a.TaskTitle
		}
		if a.ProjectSlug != "" {
			row.Project, row.ProjectHref = a.ProjectName, base+"/projetos/"+a.ProjectSlug
		}
		if a.PersonaSlug != "" {
			row.Persona, row.PersonaHref = a.PersonaName, base+"/personas/"+a.PersonaSlug
		}
		vm.Rows = append(vm.Rows, row)
	}
	sh := s.shell(r, co, "artefatos", i18n.T(ctx, "Artefatos"), views.Crumb{Label: i18n.T(ctx, "Artefatos")})
	s.render(w, r, http.StatusOK, views.ArtifactsPage(sh, vm))
}

func cloneQuery(q url.Values) url.Values {
	out := make(url.Values, len(q))
	for k, v := range q {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// ---------- envio ----------

func (s *server) blankArtifactForm(r *http.Request, co domain.Company, maxFile int64) views.ArtifactFormVM {
	base := "/c/" + co.Slug
	ctx := r.Context()
	return views.ArtifactFormVM{Action: base + "/artefatos/novo", CancelHref: base + "/artefatos", FieldErr: map[string]string{},
		ProjectOptions: s.taskProjectOptions(r, co, nil), MaxFile: fmtBytes(ctx, maxFile)}
}

func (s *server) renderArtifactForm(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.ArtifactFormVM) {
	base := "/c/" + co.Slug
	ctx := r.Context()
	sh := s.shell(r, co, "artefatos", i18n.T(ctx, "Enviar artefato"), views.Crumb{Label: i18n.T(ctx, "Artefatos"), Href: base + "/artefatos"}, views.Crumb{Label: i18n.T(ctx, "Enviar")})
	s.render(w, r, status, views.ArtifactFormPage(sh, vm))
}

func (s *server) handleNewArtifact(w http.ResponseWriter, r *http.Request, co domain.Company) {
	usage, err := s.be.GetArtifactUsage(r.Context(), co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm := s.blankArtifactForm(r, co, usage.MaxFileBytes)
	vm.Task = strings.TrimSpace(r.URL.Query().Get("task"))
	if ref := strings.TrimSpace(r.URL.Query().Get("projeto")); ref != "" {
		for i := range vm.ProjectOptions {
			vm.ProjectOptions[i].Selected = vm.ProjectOptions[i].Slug == ref
		}
	}
	s.renderArtifactForm(w, r, co, http.StatusOK, vm)
}

// artifactFormError põe o erro do service no campo certo do formulário.
func artifactFormError(ctx context.Context, vm *views.ArtifactFormVM, err error) {
	var lim *service.ArtifactLimitError
	if errors.As(err, &lim) {
		vm.FieldErr["file"] = artifactLimitMessage(ctx, lim)
		vm.Error = vm.FieldErr["file"]
		return
	}
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		field := ve.Field
		msg := ve.Field + ": " + ve.Reason + "."
		switch field {
		case "name":
			msg = i18n.T(ctx, "Nome inválido: use até 200 caracteres, sem barras.")
		case "task_id", "task":
			field, msg = "task", i18n.T(ctx, "Task não encontrada nesta empresa.")
		}
		vm.FieldErr[field] = msg
		vm.Error = msg
		return
	}
	_, title, msg := friendly(ctx, err)
	vm.Error = title + ". " + msg
}

// uploadName é o nome do artefato: o campo "nome" ou, na falta, o nome do arquivo sem diretórios (o navegador pode
// mandar um caminho do cliente). Nunca vira caminho no servidor: o conteúdo mora no banco.
func uploadName(field, filename string) string {
	if n := strings.TrimSpace(field); n != "" {
		return n
	}
	n := path.Base(strings.ReplaceAll(filename, "\\", "/"))
	if n == "." || n == "/" {
		return ""
	}
	return n
}

func (s *server) handleUploadArtifact(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	usage, err := s.be.GetArtifactUsage(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm := s.blankArtifactForm(r, co, usage.MaxFileBytes)
	bad := func(err error) {
		artifactFormError(ctx, &vm, err)
		s.renderArtifactForm(w, r, co, statusFor(err), vm)
	}

	r.Body = http.MaxBytesReader(w, r.Body, usage.MaxFileBytes+uploadFormOverhead)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			artifactFormError(ctx, &vm, service.NewArtifactLimitError(false, usage.MaxFileBytes, 0, mbe.Limit))
			s.renderArtifactForm(w, r, co, http.StatusRequestEntityTooLarge, vm)
			return
		}
		bad(domain.Invalid("file", i18n.T(ctx, "envio inválido")))
		return
	}
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	vm.Name, vm.Task = trimForm(r, "name"), trimForm(r, "task")
	selectOption(vm.ProjectOptions, trimForm(r, "project"))

	file, hdr, err := r.FormFile("file")
	if err != nil {
		bad(domain.Invalid("file", i18n.T(ctx, "escolha um arquivo")))
		return
	}
	defer func() { _ = file.Close() }()
	if hdr.Size > usage.MaxFileBytes {
		bad(service.NewArtifactLimitError(false, usage.MaxFileBytes, 0, hdr.Size))
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, usage.MaxFileBytes+1))
	if err != nil {
		bad(domain.Invalid("file", i18n.T(ctx, "não foi possível ler o arquivo")))
		return
	}

	in := service.PutArtifactInput{Name: uploadName(vm.Name, hdr.Filename), Content: data}
	if vm.Task != "" {
		t, err := s.be.GetTask(ctx, co.ID, vm.Task)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				err = domain.Invalid("task", "not found")
			}
			bad(err)
			return
		}
		in.TaskID = &t.ID
	}
	pid, ok := parseID(trimForm(r, "project"))
	if !ok {
		bad(domain.Invalid("project", "invalid"))
		return
	}
	in.ProjectID = pid
	av, err := s.be.PutArtifact(ctx, actorFrom(ctx), co.ID, in)
	if err != nil {
		bad(err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/artefatos/"+av.ID.String()+"?ok=enviado")
}

// ---------- detalhe ----------

func artifactID(r *http.Request) (domain.ID, bool) {
	id, err := domain.ParseID(r.PathValue("artifact"))
	return id, err == nil
}

// previewOf prepara a prévia: o texto (cortado em artifactPreviewMax, em fronteira de caractere) ou a imagem.
func (s *server) previewOf(ctx context.Context, co domain.Company, a service.ArtifactView, vm *views.ArtifactVM) error {
	switch domain.PreviewOf(a.Mime) {
	case domain.PreviewImage:
		vm.Preview = string(domain.PreviewImage)
		return nil
	case domain.PreviewNone:
		vm.Preview = string(domain.PreviewNone)
		return nil
	}
	_, content, err := s.be.ArtifactContent(ctx, co.ID, a.ID)
	if err != nil {
		return err
	}
	if len(content) > artifactPreviewMax {
		content, vm.Truncated = content[:artifactPreviewMax], true
		for i := 0; i < utf8.UTFMax && !utf8.Valid(content) && len(content) > 0; i++ {
			content = content[:len(content)-1] // corta um caractere UTF-8 partido ao meio
		}
	}
	if !utf8.Valid(content) {
		vm.Preview, vm.Truncated = string(domain.PreviewNone), false
		return nil
	}
	if domain.PreviewOf(a.Mime) == domain.PreviewMarkdown {
		vm.Preview, vm.PreviewHTML = string(domain.PreviewMarkdown), renderMD(string(content))
		return nil
	}
	vm.Preview, vm.PreviewText = string(domain.PreviewText), string(content)
	return nil
}

func diffLines(res textdiff.Result) ([]views.DiffLine, int) {
	lines := textdiff.Context(res.Lines, 3)
	cut := 0
	if len(lines) > diffRowsMax {
		cut = len(lines) - diffRowsMax
		lines = lines[:diffRowsMax]
	}
	out := make([]views.DiffLine, 0, len(lines))
	for _, l := range lines {
		d := views.DiffLine{Op: string(rune(l.Op)), Text: l.Text, Skipped: l.Skipped}
		if l.OldNo > 0 {
			d.OldNo = strconv.Itoa(l.OldNo)
		}
		if l.NewNo > 0 {
			d.NewNo = strconv.Itoa(l.NewNo)
		}
		out = append(out, d)
	}
	return out, cut
}

func (s *server) handleArtifact(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	id, ok := artifactID(r)
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	a, err := s.be.GetArtifactView(ctx, co.ID, id)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	versions, err := s.be.ArtifactVersions(ctx, co.ID, id)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	personas, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	names := s.lookup(ctx, personas, nil)
	base := "/c/" + co.Slug
	loc := loadLoc(co.Timezone)
	self := base + "/artefatos/" + a.ID.String()

	vm := views.ArtifactVM{Name: a.Name, Mime: a.Mime, Kind: artifactKindLabel(ctx, a.Mime), Size: fmtBytes(ctx, a.SizeBytes), Version: a.Version,
		SHA: a.SHA256, Created: fmtDateTime(ctx, a.CreatedAt, loc), Author: names.actorName(a.Author),
		ContentHref: self + "/conteudo", DownloadHref: self + "/conteudo?baixar=1", DeleteHref: self + "/excluir", ListHref: base + "/artefatos",
		Notice: i18n.T(ctx, artifactNotices[r.URL.Query().Get("ok")])}
	if a.TaskKey != "" {
		vm.TaskKey, vm.TaskHref, vm.TaskTitle = a.TaskKey, base+"/tasks/"+a.TaskKey, a.TaskTitle
	}
	if a.ProjectSlug != "" {
		vm.Project, vm.ProjectHref = a.ProjectName, base+"/projetos/"+a.ProjectSlug
	}
	if a.PersonaSlug != "" {
		vm.Persona, vm.PersonaHref = a.PersonaName, base+"/personas/"+a.PersonaSlug
	}
	if a.RunID != nil {
		vm.Run, vm.RunHref = a.RunName, base+"/execucoes/"+a.RunID.String()
		if vm.Run == "" {
			vm.Run = shortID(*a.RunID)
		}
	}
	vm.Latest = len(versions) == 0 || versions[0].ID == a.ID
	if len(versions) > 0 {
		vm.LatestHref = base + "/artefatos/" + versions[0].ID.String()
	}
	contra, _ := domain.ParseID(r.URL.Query().Get("contra"))
	for i, v := range versions {
		row := views.ArtifactVersionRow{Version: v.Version, Href: base + "/artefatos/" + v.ID.String(), Size: fmtBytes(ctx, v.SizeBytes),
			When: fmtDateTime(ctx, v.CreatedAt, loc), Author: names.actorName(v.Author), SHA: v.SHA256, Current: v.ID == a.ID}
		if v.RunID != nil {
			row.Run, row.RunHref = shortID(*v.RunID), base+"/execucoes/"+v.RunID.String()
		}
		if i+1 < len(versions) && domain.IsText(v.Mime) && domain.IsText(versions[i+1].Mime) {
			row.DiffHref = base + "/artefatos/" + v.ID.String() + "?contra=" + versions[i+1].ID.String() + "#diff"
		}
		vm.Versions = append(vm.Versions, row)
	}
	if err := s.previewOf(ctx, co, a, &vm); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if contra != (domain.ID{}) {
		d := &views.DiffVM{ToVersion: a.Version, CloseHref: self}
		res, err := s.be.ArtifactDiff(ctx, co.ID, contra, a.ID)
		for _, v := range versions {
			if v.ID == contra {
				d.FromVersion = v.Version
			}
		}
		switch {
		case errors.Is(err, domain.ErrInvalid):
			d.Error = i18n.T(ctx, "Não foi possível comparar estas duas versões: são de artefatos diferentes, não são texto ou são grandes demais.")
		case err != nil:
			_, _, d.Error = friendly(ctx, err)
		default:
			d.Added, d.Removed, d.Coarse, d.Same = res.Added, res.Removed, res.Coarse, !res.Changed()
			d.Lines, d.Cut = diffLines(res)
		}
		vm.Diff = d
	}
	sh := s.shell(r, co, "artefatos", a.Name, views.Crumb{Label: i18n.T(ctx, "Artefatos"), Href: base + "/artefatos"}, views.Crumb{Label: a.Name})
	s.render(w, r, http.StatusOK, views.ArtifactPage(sh, vm))
}

// handleArtifactContent entrega os bytes com os cabeçalhos de segurança (nosniff, CSP, attachment fora dos tipos seguros).
func (s *server) handleArtifactContent(w http.ResponseWriter, r *http.Request, co domain.Company) {
	id, ok := artifactID(r)
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	a, content, err := s.be.ArtifactContent(r.Context(), co.ID, id)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	artifactsrv.ServeAs(w, a, content, r.URL.Query().Get("baixar") != "")
}

func (s *server) handleDeleteArtifact(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	id, ok := artifactID(r)
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	if err := s.be.DeleteArtifact(ctx, actorFrom(ctx), co.ID, id); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/artefatos?ok=excluido")
}
