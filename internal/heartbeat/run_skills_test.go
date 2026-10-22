package heartbeat

import (
	"context"
	"errors"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// skillsRecorderBackend é o fakeBackend que também registra as skills da run (ADR-0044).
type skillsRecorderBackend struct {
	*fakeBackend
	got  []domain.Skill
	runs []domain.ID
	err  error
}

func (b *skillsRecorderBackend) RecordRunSkills(_ context.Context, _, runID domain.ID, skills []domain.Skill) error {
	b.runs = append(b.runs, runID)
	b.got = append(b.got, skills...)
	return b.err
}

func TestRecordRunSkills(t *testing.T) {
	be := &skillsRecorderBackend{fakeBackend: &fakeBackend{}}
	h, _ := newTestHB(t, be.fakeBackend, okAdapter(), &memStub{}, Options{PublicURL: "http://h:7420"})
	h.svc = be
	rc := sampleContext()
	var events []string
	emit := func(_ domain.RunEventKind, text string) { events = append(events, text) }

	// sem skills: nada a registrar
	h.recordRunSkills(context.Background(), rc, service.RunExtras{}, emit)
	if len(be.runs) != 0 {
		t.Fatal("sem skills não deveria chamar o backend")
	}
	h.recordRunSkills(context.Background(), rc, service.RunExtras{Skills: twoSkills()}, emit)
	if len(be.runs) != 1 || be.runs[0] != rc.Run.ID || len(be.got) != 2 || be.got[0].Slug != "deploy" {
		t.Fatalf("registro: %+v %+v", be.runs, be.got)
	}
	if len(events) != 0 {
		t.Fatalf("eventos inesperados: %v", events)
	}
	// falha vira aviso no log da run e não derruba nada
	be.err = errors.New("banco fora")
	h.recordRunSkills(context.Background(), rc, service.RunExtras{Skills: twoSkills()}, emit)
	if len(events) != 1 {
		t.Fatalf("a falha deveria virar um aviso: %v", events)
	}
	// backend que não sabe registrar: sem efeito
	h2, _ := newTestHB(t, &fakeBackend{}, okAdapter(), &memStub{}, Options{PublicURL: "http://h:7420"})
	h2.recordRunSkills(context.Background(), rc, service.RunExtras{Skills: twoSkills()}, emit)
}
