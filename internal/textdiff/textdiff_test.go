package textdiff

import (
	"strings"
	"testing"
)

func render(r Result) string {
	var sb strings.Builder
	for _, l := range r.Lines {
		sb.WriteString(string(l.Op) + l.Text + "\n")
	}
	return sb.String()
}

func TestLines(t *testing.T) {
	for name, c := range map[string]struct {
		a, b       string
		want       string
		add, remov int
	}{
		"igual":      {"a\nb\n", "a\nb\n", " a\n b\n", 0, 0},
		"inserção":   {"a\nc\n", "a\nb\nc\n", " a\n+b\n c\n", 1, 0},
		"remoção":    {"a\nb\nc\n", "a\nc\n", " a\n-b\n c\n", 0, 1},
		"troca":      {"a\nb\nc\n", "a\nX\nc\n", " a\n-b\n+X\n c\n", 1, 1},
		"vazio-novo": {"", "a\nb\n", "+a\n+b\n", 2, 0},
		"novo-vazio": {"a\n", "", "-a\n", 0, 1},
		"crlf":       {"a\r\nb\r\n", "a\nb\n", " a\n b\n", 0, 0},
		"intercalado": {"1\n2\n3\n4\n5\n", "1\nB\n3\nD\n5\n",
			" 1\n-2\n+B\n 3\n-4\n+D\n 5\n", 2, 2},
	} {
		t.Run(name, func(t *testing.T) {
			r := Lines(c.a, c.b)
			if got := render(r); got != c.want {
				t.Errorf("diff:\n%s\nquer:\n%s", got, c.want)
			}
			if r.Added != c.add || r.Removed != c.remov {
				t.Errorf("contagens %d/%d, quer %d/%d", r.Added, r.Removed, c.add, c.remov)
			}
			if r.Changed() != (c.add+c.remov > 0) {
				t.Error("Changed")
			}
		})
	}
}

func TestLineNumbers(t *testing.T) {
	r := Lines("a\nb\nc\n", "a\nX\nY\nc\n")
	var got []string
	for _, l := range r.Lines {
		got = append(got, string(l.Op)+":"+itoa(l.OldNo)+":"+itoa(l.NewNo))
	}
	want := []string{" :1:1", "-:2:0", "+:0:2", "+:0:3", " :3:4"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("numeração %v, quer %v", got, want)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

func TestCoarseWhenTooBig(t *testing.T) {
	var a, b strings.Builder
	for i := 0; i < 2500; i++ {
		a.WriteString("a" + itoa(i) + "\n")
		b.WriteString("b" + itoa(i) + "\n")
	}
	r := Lines(a.String(), b.String())
	if !r.Coarse || r.Removed != 2500 || r.Added != 2500 {
		t.Fatalf("coarse=%v removed=%d added=%d", r.Coarse, r.Removed, r.Added)
	}
}

func TestContext(t *testing.T) {
	var a, b strings.Builder
	for i := 1; i <= 20; i++ {
		a.WriteString("l" + itoa(i) + "\n")
		if i == 10 {
			b.WriteString("changed\n")
		} else {
			b.WriteString("l" + itoa(i) + "\n")
		}
	}
	got := Context(Lines(a.String(), b.String()).Lines, 2)
	// 2 de contexto antes, a troca (2 linhas), 2 depois, e um Skip de cada lado
	if len(got) != 1+2+2+2+1 {
		t.Fatalf("%d linhas: %+v", len(got), got)
	}
	if got[0].Op != Skip || got[0].Skipped != 7 || got[len(got)-1].Op != Skip || got[len(got)-1].Skipped != 8 {
		t.Errorf("omissões: %+v … %+v", got[0], got[len(got)-1])
	}
	if all := Context(Lines("a\n", "a\n").Lines, 3); len(all) != 1 || all[0].Op != Skip {
		t.Errorf("sem mudança deve virar um Skip: %+v", all)
	}
}
