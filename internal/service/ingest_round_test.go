package service

import (
	"testing"

	"github.com/renatobardi/hanko/internal/ingest"
)

func stepAttrs(kind, review, rev, key string) swarmEvent {
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	return swarmEvent{name: eventStepPublished, round: "swarm-1", attrs: map[string]string{
		"oute.swarm.step.kind": kind, "oute.swarm.step.review": review, "oute.swarm.step.rev": rev, "oute.swarm.step.key": key,
		"oute.swarm.step.sha256": sha, "oute.swarm.step.refcheck": "ok", "oute.swarm.cycle": "renatobardi/hanko#126"}, row: ingest.LogRow{}}
}

func TestParseStep(t *testing.T) {
	s, ok := parseStep(stepAttrs("triagem", "aprovado", "2", ""))
	if !ok || s.kind != "triage" || s.review != "approved" || s.rev != 2 || s.check != "ok" || s.cycle != "renatobardi/hanko#126" {
		t.Fatalf("triagem válida = %+v (%v)", s, ok)
	}
	if s, ok = parseStep(stepAttrs("merge", "sem-revisor", "1", "42")); !ok || s.kind != "merge" || s.key != "42" || s.review != "no_reviewer" {
		t.Errorf("merge válido = %+v (%v)", s, ok)
	}
	for name, ev := range map[string]swarmEvent{
		"tipo desconhecido":   stepAttrs("foo", "aprovado", "1", ""),
		"veredito inválido":   stepAttrs("triagem", "talvez", "1", ""),
		"revisão zero":        stepAttrs("triagem", "aprovado", "0", ""),
		"revisão acima":       stepAttrs("triagem", "aprovado", "10000", ""),
		"merge sem chave":     stepAttrs("merge", "aprovado", "1", ""),
		"chave fora do merge": stepAttrs("kaizen", "aprovado", "1", "7"),
		"chave inválida":      stepAttrs("merge", "aprovado", "1", "007"),
	} {
		if _, ok := parseStep(ev); ok {
			t.Errorf("%s deveria ser recusado", name)
		}
	}
	bad := stepAttrs("triagem", "aprovado", "1", "")
	bad.attrs["oute.swarm.step.sha256"] = "xyz"
	if _, ok := parseStep(bad); ok {
		t.Error("sha256 inválido deveria ser recusado")
	}
	odd := stepAttrs("triagem", "aprovado", "1", "")
	odd.attrs["oute.swarm.step.refcheck"] = "talvez"
	odd.attrs["oute.swarm.cycle"] = "sem-issue"
	if s, ok := parseStep(odd); !ok || s.check != "missing" || s.cycle != "" {
		t.Errorf("conferência e ciclo inválidos viram missing e vazio: %+v (%v)", s, ok)
	}
}

func TestAttrStrings(t *testing.T) {
	m := attrStrings(`{"a":"x","n":12,"f":1.5,"o":{"k":1},"b":true}`)
	if m["a"] != "x" || m["n"] != "12" || m["f"] != "1.5" {
		t.Errorf("escalares = %v", m)
	}
	if _, ok := m["o"]; ok {
		t.Error("objeto não é escalar")
	}
	if attrStrings(`não é json`) != nil {
		t.Error("JSON inválido deveria dar nil")
	}
}

func TestRoundFactsNote(t *testing.T) {
	f := &roundFacts{}
	ev := func(name string, at int64, kv ...string) swarmEvent {
		a := map[string]string{"oute.swarm.round": "swarm-1"}
		for i := 0; i < len(kv); i += 2 {
			a[kv[i]] = kv[i+1]
		}
		return swarmEvent{name: name, round: "swarm-1", at: at, attrs: a}
	}
	f.note(ev(eventRoundOpened, 50, "oute.swarm.repo", "dono/novo", "oute.swarm.max", "3", "oute.swarm.round.name", "Brave_Otter"))
	f.note(ev(eventRoundOpened, 20, "oute.swarm.repo", "dono/antigo", "oute.swarm.max", "5"))
	f.note(ev(eventRoundClosed, 90))
	f.note(ev(eventRoundClosed, 70))
	f.note(ev(eventSessionSpawn, 30, "oute.swarm.session", "w1", "oute.swarm.issue", "dono/repo#7"))
	f.note(ev(eventSessionClosed, 80, "oute.swarm.session", "w1"))
	if f.repo.text != "dono/antigo" || f.max.text != "5" {
		t.Errorf("o fato mais antigo vence: repo %q max %q", f.repo.text, f.max.text)
	}
	if f.opened == nil || *f.opened != 20 || f.closed == nil || *f.closed != 90 {
		t.Errorf("abertura = %v, fechamento = %v, quer 20 e 90", f.opened, f.closed)
	}
	p := f.participants["w1"]
	if p == nil || p.issue != "dono/repo#7" || *p.spawned != 30 || *p.close != 80 {
		t.Errorf("participante = %+v", p)
	}
	if f.name != "Brave_Otter" {
		t.Errorf("nome da rodada = %q", f.name)
	}
}
