package api

import (
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func TestGateOutRevisao(t *testing.T) {
	run := domain.NewID()
	g := service.GateView{Gate: domain.Gate{ID: domain.NewID(), Review: &domain.GateReview{Reviewer: "Kenji", Verdict: domain.ReviewRejected,
		Justification: "Faltam testes.", ArtifactSHA256: "abc", RunID: &run}}}
	o := gateOut(g)
	if o.Review == nil || o.Review.Verdict != "rejected" || o.Review.RunID != run.String() || o.Review.ArtifactSHA256 != "abc" {
		t.Fatalf("review: %+v", o.Review)
	}
	if gateOut(service.GateView{}).Review != nil {
		t.Fatal("sem revisão não expõe review")
	}
	rev := domain.NewID()
	p := personaOut(domain.Persona{ReviewerPersonaID: &rev, ReviewerAgent: "", ReviewerModel: "m"})
	if p.ReviewerPersonaID != rev.String() || p.ReviewerModel != "m" {
		t.Fatalf("persona: %+v", p)
	}
}
