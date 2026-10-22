package uitest

import (
	"context"
	"fmt"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Dublê dos gatilhos, revisões e arquivar de fluxos (ADR-0047).

func (f *Fake) flowIndex(flowID domain.ID) int {
	for i, v := range f.Flows {
		if v.ID == flowID {
			return i
		}
	}
	return -1
}

// ListFlowsFiltered lista os fluxos ativos ou os arquivados.
func (f *Fake) ListFlowsFiltered(_ context.Context, _ domain.ID, archived bool) ([]service.FlowView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListFlowsFiltered"); err != nil {
		return nil, err
	}
	var out []service.FlowView
	for _, v := range f.Flows {
		if (v.ArchivedAt != nil) == archived {
			out = append(out, f.withTriggers(v))
		}
	}
	return out, nil
}

func (f *Fake) withTriggers(v service.FlowView) service.FlowView {
	v.Triggers = nil
	for _, t := range f.FlowTriggers {
		if t.FlowID == v.ID {
			v.Triggers = append(v.Triggers, t)
		}
	}
	return v
}

// RunFlowNowWith confere as obrigatórias e dispara como RunFlowNow.
func (f *Fake) RunFlowNowWith(ctx context.Context, actor domain.Actor, companyID, flowID domain.ID, vars map[string]string) (service.FlowFireResult, error) {
	f.mu.Lock()
	i := f.flowIndex(flowID)
	if i < 0 {
		f.mu.Unlock()
		return service.FlowFireResult{}, domain.ErrNotFound
	}
	decl := f.Flows[i].Variables
	f.mu.Unlock()
	f.note("RunFlowNowWith")
	if _, err := domain.ResolveFlowVariables(decl, vars, true); err != nil {
		return service.FlowFireResult{}, err
	}
	res, err := f.RunFlowNow(ctx, actor, companyID, flowID)
	if err == nil {
		f.mu.Lock()
		for k := range f.Fires {
			if f.Fires[k].ID == res.Fire.ID {
				f.Fires[k].Variables = vars
				f.Fires[k].Source = domain.FireManual
			}
		}
		f.mu.Unlock()
	}
	return res, err
}

// ArchiveFlow arquiva ou desarquiva.
func (f *Fake) ArchiveFlow(_ context.Context, _ domain.Actor, _, flowID domain.ID, archive bool) (service.FlowView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("ArchiveFlow")
	if err := f.fail("ArchiveFlow"); err != nil {
		return service.FlowView{}, err
	}
	i := f.flowIndex(flowID)
	if i < 0 {
		return service.FlowView{}, domain.ErrNotFound
	}
	fl := f.Flows[i].Flow
	if archive {
		n := Now()
		fl.ArchivedAt = &n
	} else {
		fl.ArchivedAt = nil
	}
	fl.Enabled, fl.NextFireAt = false, nil
	f.Flows[i] = f.flowView(fl)
	return f.Flows[i], nil
}

// CreateFlowTrigger cria um gatilho; o webhook ganha um segredo gerado.
func (f *Fake) CreateFlowTrigger(_ context.Context, _ domain.Actor, companyID, flowID domain.ID, in service.CreateFlowTriggerInput) (service.FlowTriggerView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("CreateFlowTrigger")
	if err := f.fail("CreateFlowTrigger"); err != nil {
		return service.FlowTriggerView{}, err
	}
	i := f.flowIndex(flowID)
	if i < 0 {
		return service.FlowTriggerView{}, domain.ErrNotFound
	}
	if !in.Kind.Valid() || strings.TrimSpace(in.Name) == "" {
		return service.FlowTriggerView{}, domain.Invalid("name", "obrigatório")
	}
	for _, t := range f.FlowTriggers {
		if t.FlowID == flowID && t.Name == in.Name {
			return service.FlowTriggerView{}, fmt.Errorf("trigger %q: %w", in.Name, domain.ErrConflict)
		}
	}
	rate := in.RatePerMin
	if rate == 0 {
		rate = domain.DefaultTriggerRatePerMin
	}
	t := domain.FlowTrigger{ID: domain.NewID(), CompanyID: companyID, FlowID: flowID, Kind: in.Kind, Name: in.Name, Enabled: true,
		SecretRef: in.SecretRef, RatePerMin: rate, EventVerb: in.EventVerb, EventFilter: in.EventFilter, Mapping: in.Mapping, CreatedAt: Now()}
	v := service.FlowTriggerView{FlowTrigger: t}
	if in.Kind == domain.TriggerKindWebhook {
		v.Path = service.FlowWebhookPath(t.ID)
		if in.SecretRef == "" {
			t.SecretRef = "${secret:hook-" + f.Flows[i].Slug + "-1234abcd}"
			v.SecretRef, v.SecretValue = t.SecretRef, "hk_segredo-de-teste"
		}
	}
	f.FlowTriggers = append(f.FlowTriggers, t)
	return v, nil
}

// UpdateFlowTrigger muda o estado de um gatilho.
func (f *Fake) UpdateFlowTrigger(_ context.Context, _ domain.Actor, _, flowID, triggerID domain.ID, in service.UpdateFlowTriggerInput) (service.FlowTriggerView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("UpdateFlowTrigger")
	if err := f.fail("UpdateFlowTrigger"); err != nil {
		return service.FlowTriggerView{}, err
	}
	for i, t := range f.FlowTriggers {
		if t.ID == triggerID && t.FlowID == flowID {
			if in.Enabled != nil {
				t.Enabled = *in.Enabled
			}
			if in.Name != nil {
				t.Name = *in.Name
			}
			f.FlowTriggers[i] = t
			return service.FlowTriggerView{FlowTrigger: t}, nil
		}
	}
	return service.FlowTriggerView{}, domain.ErrNotFound
}

// DeleteFlowTrigger remove um gatilho.
func (f *Fake) DeleteFlowTrigger(_ context.Context, _ domain.Actor, _, flowID, triggerID domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("DeleteFlowTrigger")
	if err := f.fail("DeleteFlowTrigger"); err != nil {
		return err
	}
	for i, t := range f.FlowTriggers {
		if t.ID == triggerID && t.FlowID == flowID {
			f.FlowTriggers = append(f.FlowTriggers[:i], f.FlowTriggers[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

// FlowRevisions devolve as revisões gravadas no dublê (mais nova primeiro); sem nenhuma, a revisão 1 sintética.
func (f *Fake) FlowRevisions(_ context.Context, _, flowID domain.ID, _ int) ([]domain.FlowRevision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("FlowRevisions"); err != nil {
		return nil, err
	}
	var out []domain.FlowRevision
	for i := len(f.FlowRevs) - 1; i >= 0; i-- {
		if f.FlowRevs[i].FlowID == flowID {
			out = append(out, f.FlowRevs[i])
		}
	}
	if len(out) == 0 {
		if i := f.flowIndex(flowID); i >= 0 {
			fl := f.Flows[i]
			out = append(out, domain.FlowRevision{ID: domain.NewID(), FlowID: flowID, Revision: 1, Origin: "create", Actor: domain.HumanActor,
				CreatedAt: fl.CreatedAt, Snapshot: domain.FlowSnapshot{Name: fl.Name, Cron: fl.Cron, Timezone: fl.Timezone, PersonaID: fl.PersonaID,
					Priority: string(fl.Priority), TaskTitle: fl.TaskTitle, Variables: fl.Variables}})
		}
	}
	return out, nil
}

// RestoreFlowRevision registra a restauração como revisão nova.
func (f *Fake) RestoreFlowRevision(_ context.Context, _ domain.Actor, _, flowID domain.ID, revision int) (service.FlowView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("RestoreFlowRevision")
	if err := f.fail("RestoreFlowRevision"); err != nil {
		return service.FlowView{}, err
	}
	i := f.flowIndex(flowID)
	if i < 0 {
		return service.FlowView{}, domain.ErrNotFound
	}
	fl := f.Flows[i].Flow
	rev := fl.Revision + 1
	fl.Revision = rev
	f.Flows[i] = f.flowView(fl)
	from := revision
	f.FlowRevs = append(f.FlowRevs, domain.FlowRevision{ID: domain.NewID(), FlowID: flowID, Revision: rev, Origin: "restore", RestoredFrom: &from,
		Actor: domain.HumanActor, CreatedAt: Now()})
	return f.Flows[i], nil
}
