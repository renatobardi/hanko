package uitest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"mime"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/textdiff"
)

// Artefatos e metas do Fake (ADR-0028): as visões (nomes das ligações, versões, progresso, tasks por status) saem
// dos dados em memória; as validações de nome e de limites ficam no service real, aqui só o essencial.

const (
	defaultFakeMaxBytes   = 5 << 20
	defaultFakeQuotaBytes = 512 << 20
)

func (f *Fake) artifactLimits() (maxFile, quota int64) {
	maxFile, quota = f.ArtifactMaxBytes, f.ArtifactQuotaBytes
	if maxFile <= 0 {
		maxFile = defaultFakeMaxBytes
	}
	if quota <= 0 {
		quota = defaultFakeQuotaBytes
	}
	return maxFile, quota
}

func (f *Fake) artifactView(a domain.Artifact) service.ArtifactView {
	v := service.ArtifactView{Artifact: a}
	for _, o := range f.Artifacts {
		if o.Name == a.Name && sameIDPtr(o.TaskID, a.TaskID) {
			v.Versions++
		}
	}
	if a.TaskID != nil {
		for _, t := range f.Tasks {
			if t.ID == *a.TaskID {
				v.TaskKey, v.TaskTitle = t.Key, t.Title
			}
		}
	}
	if a.ProjectID != nil {
		for _, p := range f.Projects {
			if p.ID == *a.ProjectID {
				v.ProjectSlug, v.ProjectName = p.Slug, p.Name
			}
		}
	}
	if a.PersonaID != nil {
		for _, p := range f.Personas {
			if p.ID == *a.PersonaID {
				v.PersonaSlug, v.PersonaName = p.Slug, p.Name
			}
		}
	}
	if a.RunID != nil {
		for _, r := range f.Runs {
			if r.ID == *a.RunID {
				v.RunName = r.Name
			}
		}
	}
	return v
}

func sameIDPtr(a, b *domain.ID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (f *Fake) PutArtifact(_ context.Context, actor domain.Actor, companyID domain.ID, in service.PutArtifactInput) (service.ArtifactView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("PutArtifact")
	if err := f.fail("PutArtifact"); err != nil {
		return service.ArtifactView{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || strings.ContainsAny(name, "/\\") {
		return service.ArtifactView{}, domain.Invalid("name", "obrigatório, sem barras")
	}
	maxFile, quota := f.artifactLimits()
	if int64(len(in.Content)) > maxFile {
		return service.ArtifactView{}, service.NewArtifactLimitError(false, maxFile, 0, int64(len(in.Content)))
	}
	var used int64
	for _, a := range f.Artifacts {
		used += a.SizeBytes
	}
	if used+int64(len(in.Content)) > quota {
		return service.ArtifactView{}, service.NewArtifactLimitError(true, quota, used, int64(len(in.Content)))
	}
	mt := in.Mime
	if mt == "" {
		mt = mime.TypeByExtension(path.Ext(name))
		if i := strings.Index(mt, ";"); i >= 0 {
			mt = mt[:i]
		}
	}
	if mt == "" {
		mt = "application/octet-stream"
	}
	a := domain.Artifact{ID: domain.NewID(), CompanyID: companyID, TaskID: in.TaskID, RunID: in.RunID, ProjectID: in.ProjectID,
		Author: actor, Name: name, Mime: mt, SizeBytes: int64(len(in.Content)), Version: 1, CreatedAt: Now().UTC()}
	sum := sha256.Sum256(in.Content)
	a.SHA256 = hex.EncodeToString(sum[:])
	if in.TaskID != nil {
		for _, t := range f.Tasks {
			if t.ID == *in.TaskID {
				a.PersonaID = t.PersonaID
				if a.ProjectID == nil {
					a.ProjectID = t.ProjectID
				}
			}
		}
	}
	for _, o := range f.Artifacts {
		if o.Name == a.Name && sameIDPtr(o.TaskID, a.TaskID) && o.Version >= a.Version {
			a.Version = o.Version + 1
		}
	}
	f.Artifacts = append(f.Artifacts, a)
	if f.ArtifactData == nil {
		f.ArtifactData = map[domain.ID][]byte{}
	}
	f.ArtifactData[a.ID] = append([]byte(nil), in.Content...)
	return f.artifactView(a), nil
}

func (f *Fake) ListArtifacts(_ context.Context, _ domain.ID, fl service.ArtifactFilter) ([]service.ArtifactView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListArtifacts"); err != nil {
		return nil, err
	}
	newest := map[string]int{}
	key := func(a domain.Artifact) string {
		t := ""
		if a.TaskID != nil {
			t = a.TaskID.String()
		}
		return t + "\x00" + a.Name
	}
	for _, a := range f.Artifacts {
		if a.Version > newest[key(a)] {
			newest[key(a)] = a.Version
		}
	}
	var out []service.ArtifactView
	for _, a := range f.Artifacts {
		switch {
		case !fl.AllVersions && a.Version != newest[key(a)]:
			continue
		case fl.ProjectID != nil && !sameIDPtr(a.ProjectID, fl.ProjectID):
			continue
		case fl.TaskID != nil && !sameIDPtr(a.TaskID, fl.TaskID):
			continue
		case fl.PersonaID != nil && !sameIDPtr(a.PersonaID, fl.PersonaID):
			continue
		case fl.RunID != nil && !sameIDPtr(a.RunID, fl.RunID):
			continue
		case fl.Kind != "" && domain.KindOf(a.Mime) != fl.Kind:
			continue
		case fl.Query != "" && !strings.Contains(strings.ToLower(a.Name), strings.ToLower(fl.Query)):
			continue
		}
		out = append(out, f.artifactView(a))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if fl.Offset > 0 {
		if fl.Offset >= len(out) {
			return nil, nil
		}
		out = out[fl.Offset:]
	}
	if fl.Limit > 0 && len(out) > fl.Limit {
		out = out[:fl.Limit]
	}
	return out, nil
}

func (f *Fake) findArtifact(id domain.ID) (domain.Artifact, bool) {
	for _, a := range f.Artifacts {
		if a.ID == id {
			return a, true
		}
	}
	return domain.Artifact{}, false
}

func (f *Fake) GetArtifactView(_ context.Context, _, id domain.ID) (service.ArtifactView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("GetArtifactView"); err != nil {
		return service.ArtifactView{}, err
	}
	a, ok := f.findArtifact(id)
	if !ok {
		return service.ArtifactView{}, domain.ErrNotFound
	}
	return f.artifactView(a), nil
}

func (f *Fake) ArtifactVersions(_ context.Context, _, id domain.ID) ([]domain.Artifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ArtifactVersions"); err != nil {
		return nil, err
	}
	cur, ok := f.findArtifact(id)
	if !ok {
		return nil, domain.ErrNotFound
	}
	var out []domain.Artifact
	for _, a := range f.Artifacts {
		if a.Name == cur.Name && sameIDPtr(a.TaskID, cur.TaskID) {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out, nil
}

func (f *Fake) ArtifactContent(_ context.Context, _, id domain.ID) (domain.Artifact, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ArtifactContent"); err != nil {
		return domain.Artifact{}, nil, err
	}
	a, ok := f.findArtifact(id)
	if !ok {
		return domain.Artifact{}, nil, domain.ErrNotFound
	}
	return a, append([]byte(nil), f.ArtifactData[id]...), nil
}

func (f *Fake) ArtifactDiff(_ context.Context, _, from, to domain.ID) (textdiff.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ArtifactDiff"); err != nil {
		return textdiff.Result{}, err
	}
	a, ok1 := f.findArtifact(from)
	b, ok2 := f.findArtifact(to)
	if !ok1 || !ok2 {
		return textdiff.Result{}, domain.ErrNotFound
	}
	ab, bb := f.ArtifactData[from], f.ArtifactData[to]
	switch {
	case a.Name != b.Name || !sameIDPtr(a.TaskID, b.TaskID):
		return textdiff.Result{}, domain.Invalid("to", "versions belong to different artifacts")
	case !domain.IsText(a.Mime) || !domain.IsText(b.Mime):
		return textdiff.Result{}, domain.Invalid("mime", "diff is only available for text")
	case !utf8.Valid(ab) || !utf8.Valid(bb):
		return textdiff.Result{}, domain.Invalid("content", "not valid UTF-8 text")
	}
	return textdiff.Lines(string(ab), string(bb)), nil
}

func (f *Fake) DeleteArtifact(_ context.Context, _ domain.Actor, _, id domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("DeleteArtifact")
	if err := f.fail("DeleteArtifact"); err != nil {
		return err
	}
	cur, ok := f.findArtifact(id)
	if !ok {
		return domain.ErrNotFound
	}
	kept := f.Artifacts[:0:0]
	for _, a := range f.Artifacts {
		if a.Name == cur.Name && sameIDPtr(a.TaskID, cur.TaskID) {
			delete(f.ArtifactData, a.ID)
			continue
		}
		kept = append(kept, a)
	}
	f.Artifacts = kept
	return nil
}

func (f *Fake) GetArtifactUsage(context.Context, domain.ID) (service.ArtifactUsage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("GetArtifactUsage"); err != nil {
		return service.ArtifactUsage{}, err
	}
	maxFile, quota := f.artifactLimits()
	u := service.ArtifactUsage{QuotaBytes: quota, MaxFileBytes: maxFile, Versions: len(f.Artifacts)}
	for _, a := range f.Artifacts {
		u.UsedBytes += a.SizeBytes
	}
	return u, nil
}

// ---------- metas ----------

func (f *Fake) goalView(g domain.Goal, detail bool) service.GoalView {
	v := service.GoalView{Goal: g, ProgressPercent: g.Progress() * 100, Tasks: map[domain.TaskStatus]int{}}
	if g.ProjectID != nil {
		for _, p := range f.Projects {
			if p.ID == *g.ProjectID {
				v.ProjectSlug, v.ProjectName = p.Slug, p.Name
			}
		}
	}
	if g.OwnerPersonaID != nil {
		for _, p := range f.Personas {
			if p.ID == *g.OwnerPersonaID {
				v.OwnerSlug, v.OwnerName = p.Slug, p.Name
			}
		}
	}
	for _, t := range f.Tasks {
		if t.GoalID != nil && *t.GoalID == g.ID {
			v.Tasks[t.Status]++
			v.TasksTotal++
		}
	}
	if detail {
		for _, u := range f.GoalUpdates {
			if u.GoalID == g.ID {
				v.Updates = append(v.Updates, u)
			}
		}
		sort.SliceStable(v.Updates, func(i, j int) bool { return v.Updates[i].CreatedAt.Before(v.Updates[j].CreatedAt) })
	}
	return v
}

func (f *Fake) findGoal(ref string) (int, bool) {
	for i, g := range f.Goals {
		if g.Slug == ref || g.ID.String() == ref {
			return i, true
		}
	}
	return 0, false
}

func (f *Fake) CreateGoal(_ context.Context, actor domain.Actor, companyID domain.ID, in service.CreateGoalInput) (domain.Goal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("CreateGoal")
	if err := f.fail("CreateGoal"); err != nil {
		return domain.Goal{}, err
	}
	if strings.TrimSpace(in.Title) == "" {
		return domain.Goal{}, domain.Invalid("title", "obrigatório")
	}
	if in.Start == in.Target {
		return domain.Goal{}, domain.Invalid("target", "deve diferir do valor inicial")
	}
	slug := in.Slug
	if slug == "" {
		slug = strings.Trim(strings.ToLower(strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
				return r
			}
			return '-'
		}, in.Title)), "-")
	}
	if _, dup := f.findGoal(slug); dup {
		return domain.Goal{}, domain.ErrConflict
	}
	now := Now().UTC()
	g := domain.Goal{ID: domain.NewID(), CompanyID: companyID, ProjectID: in.ProjectID, Slug: slug, Title: in.Title, Description: in.Description,
		Metric: in.Metric, Unit: in.Unit, StartValue: in.Start, TargetValue: in.Target, CurrentValue: in.Start, DueDate: in.DueDate,
		Status: domain.GoalActive, OwnerPersonaID: in.OwnerPersonaID, CreatedBy: actor, CreatedAt: now, UpdatedAt: now}
	if in.Current != nil {
		g.CurrentValue = *in.Current
	}
	if g.Reaches(g.CurrentValue) {
		return domain.Goal{}, domain.Invalid("current_value", "já alcança o alvo")
	}
	f.Goals = append(f.Goals, g)
	return g, nil
}

func (f *Fake) UpdateGoal(_ context.Context, _ domain.Actor, _, goalID domain.ID, in service.UpdateGoalInput) (domain.Goal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("UpdateGoal")
	if err := f.fail("UpdateGoal"); err != nil {
		return domain.Goal{}, err
	}
	i, ok := f.findGoal(goalID.String())
	if !ok {
		return domain.Goal{}, domain.ErrNotFound
	}
	g := &f.Goals[i]
	if in.Title != nil {
		if strings.TrimSpace(*in.Title) == "" {
			return domain.Goal{}, domain.Invalid("title", "obrigatório")
		}
		g.Title = *in.Title
	}
	if in.Description != nil {
		g.Description = *in.Description
	}
	if in.Metric != nil {
		g.Metric = *in.Metric
	}
	if in.Unit != nil {
		g.Unit = *in.Unit
	}
	if in.ClearProject {
		g.ProjectID = nil
	}
	if in.ProjectID != nil {
		g.ProjectID = in.ProjectID
	}
	if in.Start != nil {
		g.StartValue = *in.Start
	}
	if in.Target != nil {
		g.TargetValue = *in.Target
	}
	if g.StartValue == g.TargetValue {
		return domain.Goal{}, domain.Invalid("target", "deve diferir do valor inicial")
	}
	if in.ClearDue {
		g.DueDate = nil
	}
	if in.DueDate != nil {
		g.DueDate = in.DueDate
	}
	if in.ClearOwner {
		g.OwnerPersonaID = nil
	}
	if in.OwnerPersonaID != nil {
		g.OwnerPersonaID = in.OwnerPersonaID
	}
	if in.Status != nil {
		g.Status = *in.Status
		if g.Status == domain.GoalActive {
			g.AchievedAt = nil
		}
	}
	g.UpdatedAt = Now().UTC()
	return *g, nil
}

func (f *Fake) RecordGoalProgress(_ context.Context, actor domain.Actor, _, goalID domain.ID, value float64, note string) (service.GoalView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("RecordGoalProgress")
	if err := f.fail("RecordGoalProgress"); err != nil {
		return service.GoalView{}, err
	}
	i, ok := f.findGoal(goalID.String())
	if !ok {
		return service.GoalView{}, domain.ErrNotFound
	}
	g := &f.Goals[i]
	if g.Status != domain.GoalActive {
		return service.GoalView{}, domain.ErrConflict
	}
	now := Now().UTC()
	g.CurrentValue, g.UpdatedAt = value, now
	f.GoalUpdates = append(f.GoalUpdates, domain.GoalUpdate{ID: domain.NewID(), GoalID: g.ID, Value: value, Note: note, Author: actor, CreatedAt: now})
	if g.Reaches(value) {
		g.Status, g.AchievedAt = domain.GoalAchieved, &now
	}
	return f.goalView(*g, true), nil
}

func (f *Fake) ListGoalViews(_ context.Context, _ domain.ID, fl service.GoalFilter) ([]service.GoalView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListGoalViews"); err != nil {
		return nil, err
	}
	var out []service.GoalView
	for _, g := range f.Goals {
		if fl.Status != "" && g.Status != fl.Status {
			continue
		}
		if fl.ProjectID != nil && !sameIDPtr(g.ProjectID, fl.ProjectID) {
			continue
		}
		out = append(out, f.goalView(g, false))
	}
	return out, nil
}

func (f *Fake) GetGoalView(_ context.Context, _ domain.ID, ref string) (service.GoalView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("GetGoalView"); err != nil {
		return service.GoalView{}, err
	}
	i, ok := f.findGoal(ref)
	if !ok {
		return service.GoalView{}, domain.ErrNotFound
	}
	return f.goalView(f.Goals[i], true), nil
}

// achievedGoals são as metas atingidas nos últimos 7 dias (a Caixa de entrada); quem chama já tem o lock.
func (f *Fake) achievedGoals() []service.GoalView {
	var out []service.GoalView
	for _, g := range f.Goals {
		if g.Status == domain.GoalAchieved && g.AchievedAt != nil && Now().Sub(*g.AchievedAt) < 7*24*time.Hour {
			out = append(out, f.goalView(g, false))
		}
	}
	return out
}

// sampleArtefMetas povoa o Sample com artefatos (com versões) e metas (uma atingida, uma em andamento, uma atrasada).
func sampleArtefMetas(f *Fake, now time.Time) {
	co := f.Companies[0]
	taskByKey := func(k string) *service.TaskView {
		for i := range f.Tasks {
			if f.Tasks[i].Key == k {
				return &f.Tasks[i]
			}
		}
		return nil
	}
	put := func(k, name, mt, body string, age time.Duration, run *domain.ID, persona string) {
		t := taskByKey(k)
		a := domain.Artifact{ID: ID("artifact:" + k + ":" + name + ":" + age.String()), CompanyID: co.ID, TaskID: &t.ID, RunID: run, ProjectID: t.ProjectID,
			PersonaID: t.PersonaID, Author: domain.HumanActor, Name: name, Mime: mt, SizeBytes: int64(len(body)), Version: 1, CreatedAt: now.Add(-age)}
		if persona != "" && t.PersonaID != nil {
			a.Author = domain.PersonaActor(*t.PersonaID)
		}
		sum := sha256.Sum256([]byte(body))
		a.SHA256 = hex.EncodeToString(sum[:])
		for _, o := range f.Artifacts {
			if o.Name == name && sameIDPtr(o.TaskID, a.TaskID) && o.Version >= a.Version {
				a.Version = o.Version + 1
			}
		}
		f.Artifacts = append(f.Artifacts, a)
		if f.ArtifactData == nil {
			f.ArtifactData = map[domain.ID][]byte{}
		}
		f.ArtifactData[a.ID] = []byte(body)
	}
	r1842, r1840 := rid("7c1842"), rid("7c1840")
	put("HNK-221", "slide-01-tese.md", "text/markdown", "# Tese\n\nGate humano escala melhor que revisão de código.\n\n- gancho: o número do piloto\n", 3*time.Hour, &r1842, "ada")
	put("HNK-221", "slide-01-tese.md", "text/markdown", "# Tese\n\nGate humano escala **melhor** que revisão de código.\n\n- gancho: o número do piloto\n- fonte: relatório de setembro\n", 10*time.Minute, &r1842, "ada")
	put("HNK-218", "plano-migracao.txt", "text/plain", "1. snapshot ZFS\n2. copiar volumes\n3. trocar o ponto de montagem (irreversível)\n", 20*time.Minute, &r1840, "kenji")
	put("HNK-219", "entidades.json", "application/json", "{\"pessoas\": 41, \"organizacoes\": 17}\n", 2*time.Hour, nil, "hana")
	put("HNK-219", "grafo.png", "image/png", "\x89PNG\r\n\x1a\n", 90*time.Minute, nil, "hana")
	put("HNK-214", "relatorio.pdf", "application/pdf", "%PDF-1.4\n", 26*time.Hour, nil, "ada")

	proj := func(slug string) *domain.ID {
		for _, p := range f.Projects {
			if p.Slug == slug {
				id := p.ID
				return &id
			}
		}
		return nil
	}
	day := func(d int) *time.Time {
		t := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, d)
		return &t
	}
	ada := ID("persona:ada")
	cem := domain.Goal{ID: ID("goal:cem"), CompanyID: co.ID, ProjectID: proj("linkedin"), Slug: "cem-seguidores", Title: "Chegar a 100 seguidores", Description: "Crescer o perfil com **conteúdo semanal**.",
		Metric: "seguidores", Unit: "seg.", StartValue: 20, TargetValue: 100, CurrentValue: 65, DueDate: day(30), Status: domain.GoalActive, OwnerPersonaID: &ada,
		CreatedBy: domain.HumanActor, CreatedAt: now.AddDate(0, 0, -40), UpdatedAt: now.Add(-time.Hour)}
	lat := domain.Goal{ID: ID("goal:latencia"), CompanyID: co.ID, ProjectID: proj("kubo"), Slug: "latencia-p95", Title: "Reduzir a latência p95", Metric: "latência p95", Unit: "ms",
		StartValue: 800, TargetValue: 300, CurrentValue: 520, DueDate: day(-3), Status: domain.GoalActive, CreatedBy: domain.HumanActor, CreatedAt: now.AddDate(0, 0, -60), UpdatedAt: now.Add(-48 * time.Hour)}
	at := now.Add(-26 * time.Hour)
	dig := domain.Goal{ID: ID("goal:digest"), CompanyID: co.ID, ProjectID: proj("kubo"), Slug: "digest-no-ar", Title: "Digest semanal no ar", Metric: "digests enviados", StartValue: 0, TargetValue: 3, CurrentValue: 3,
		Status: domain.GoalAchieved, AchievedAt: &at, CreatedBy: domain.HumanActor, CreatedAt: now.AddDate(0, 0, -20), UpdatedAt: at}
	f.Goals = []domain.Goal{cem, lat, dig}
	for i, v := range []float64{20, 28, 41, 50, 65} {
		f.GoalUpdates = append(f.GoalUpdates, domain.GoalUpdate{ID: ID("goalupd:cem:" + string(rune('a'+i))), GoalID: cem.ID, Value: v,
			Author: domain.HumanActor, CreatedAt: now.AddDate(0, 0, -35+i*8)})
	}
	for i, v := range []float64{800, 700, 520} {
		f.GoalUpdates = append(f.GoalUpdates, domain.GoalUpdate{ID: ID("goalupd:lat:" + string(rune('a'+i))), GoalID: lat.ID, Value: v,
			Note: "medido no painel", Author: domain.HumanActor, CreatedAt: now.AddDate(0, 0, -50+i*20)})
	}
	for _, k := range []string{"HNK-221", "HNK-222"} {
		id := cem.ID
		taskByKey(k).GoalID = &id
	}
	id := dig.ID
	taskByKey("HNK-214").GoalID = &id
	id2 := lat.ID
	taskByKey("HNK-220").GoalID = &id2
}
