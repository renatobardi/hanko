package domain

import (
	"reflect"
	"testing"
)

func TestEffectiveInboxState(t *testing.T) {
	cases := []struct {
		name          string
		stored, now   string
		read, archive bool
		want          InboxItemState
	}{
		{"nunca marcado", "", "", false, false, InboxItemState{}},
		{"lido e igual", "a", "a", true, false, InboxItemState{Read: true}},
		{"arquivado conta como lido", "a", "a", false, true, InboxItemState{Read: true, Archived: true}},
		{"mudou: reaparece não lido", "a", "b", true, false, InboxItemState{}},
		{"mudou: reaparece desarquivado", "a", "b", true, true, InboxItemState{}},
	}
	for _, c := range cases {
		if got := EffectiveInboxState(c.stored, c.now, c.read, c.archive); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestInboxVisible(t *testing.T) {
	novo, lido, arq := InboxItemState{}, InboxItemState{Read: true}, InboxItemState{Read: true, Archived: true}
	cases := []struct {
		v    InboxView
		st   InboxItemState
		want bool
	}{
		{InboxViewAll, novo, true}, {InboxViewAll, lido, true}, {InboxViewAll, arq, false},
		{InboxViewUnread, novo, true}, {InboxViewUnread, lido, false}, {InboxViewUnread, arq, false},
		{InboxViewArchived, novo, false}, {InboxViewArchived, lido, false}, {InboxViewArchived, arq, true},
		{"", novo, true}, {"", arq, false},
	}
	for _, c := range cases {
		if got := InboxVisible(c.v, c.st); got != c.want {
			t.Errorf("view %q state %+v: got %v", c.v, c.st, got)
		}
	}
}

func TestInboxEnums(t *testing.T) {
	for _, k := range InboxItemKinds {
		if !k.Valid() {
			t.Errorf("%s deveria ser válido", k)
		}
	}
	if InboxItemKind("x").Valid() || InboxView("x").Valid() || InboxAction("x").Valid() || NotifyKind("x").Valid() {
		t.Error("valor desconhecido não pode ser válido")
	}
	for _, v := range []InboxView{InboxViewAll, InboxViewUnread, InboxViewArchived} {
		if !v.Valid() {
			t.Errorf("view %s", v)
		}
	}
	for _, a := range []InboxAction{InboxRead, InboxUnread, InboxArchive, InboxUnarchive, InboxReadAll} {
		if !a.Valid() {
			t.Errorf("action %s", a)
		}
	}
	for _, k := range NotifyKinds {
		if !k.Valid() {
			t.Errorf("notify %s", k)
		}
	}
}

func TestDefaultNotifyPref(t *testing.T) {
	for _, k := range NotifyKinds {
		p := DefaultNotifyPref(k)
		if !p.Toast {
			t.Errorf("%s: toast deveria vir ligado", k)
		}
		wantPush := k == NotifyMention || k == NotifyGate
		if p.Push != wantPush || p.Kind != k {
			t.Errorf("%s: %+v", k, p)
		}
	}
}

func TestMentionHandle(t *testing.T) {
	cases := map[string]string{
		"renato.bardi@outlook.com": "renato-bardi",
		"Ana_Paula+x@Ex.com":       "ana-paula-x",
		"  bruno@x.io ":            "bruno",
		"--a..b--@x":               "a-b",
		"@x.io":                    "",
		"":                         "",
		"çã@x":                     "",
	}
	for in, want := range cases {
		if got := MentionHandle(in); got != want {
			t.Errorf("MentionHandle(%q) = %q, want %q", in, got, want)
		}
	}
	long := make([]byte, 100)
	for i := range long {
		long[i] = 'a'
	}
	if got := MentionHandle(string(long) + "@x"); len(got) != 63 {
		t.Errorf("handle longo cortado em 63, veio %d", len(got))
	}
}

func TestResolveMentions(t *testing.T) {
	p1, p2, u1, u2, u3, self := NewID(), NewID(), NewID(), NewID(), NewID(), NewID()
	personas := []MentionCandidate{{p1, "ana"}, {p2, "bruno"}}
	members := []MentionCandidate{{u1, "bruno"}, {u2, "carla"}, {u3, "dup"}, {NewID(), "dup"}, {self, "eu"}, {NewID(), ""}}

	targets, amb := ResolveMentions([]string{"ana", "bruno", "carla", "dup", "eu", "ninguem", "ANA", "carla"}, personas, members, &self)
	if len(targets) != 3 {
		t.Fatalf("alvos: %+v", targets)
	}
	if targets[0].PersonaID == nil || *targets[0].PersonaID != p1 {
		t.Errorf("ana é a persona: %+v", targets[0])
	}
	if targets[1].PersonaID == nil || *targets[1].PersonaID != p2 || targets[1].UserID != nil {
		t.Errorf("persona vence membro de mesmo handle: %+v", targets[1])
	}
	if targets[2].UserID == nil || *targets[2].UserID != u2 {
		t.Errorf("carla é membro: %+v", targets[2])
	}
	if !reflect.DeepEqual(amb, []string{"dup"}) {
		t.Errorf("ambíguos: %v", amb)
	}

	// o teto de alvos
	var many []string
	var cands []MentionCandidate
	for i := 0; i < 15; i++ {
		h := string(rune('a'+i)) + "x"
		many = append(many, h)
		cands = append(cands, MentionCandidate{NewID(), h})
	}
	got, _ := ResolveMentions(many, nil, cands, nil)
	if len(got) != MaxMentionsPerComment {
		t.Errorf("teto: %d", len(got))
	}
}

func TestParticipation(t *testing.T) {
	if (Participation{}).Any() || len((Participation{}).Reasons()) != 0 {
		t.Error("vazio não participa")
	}
	p := Participation{Created: true, Assigned: true, Mentioned: true, Commented: true, Gate: true}
	want := []string{"created", "assigned", "mentioned", "commented", "gate"}
	if !p.Any() || !reflect.DeepEqual(p.Reasons(), want) {
		t.Errorf("motivos: %v", p.Reasons())
	}
	if r := (Participation{Gate: true}).Reasons(); !reflect.DeepEqual(r, []string{"gate"}) {
		t.Errorf("só gate: %v", r)
	}
}
