package views

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

var sh = Shell{Title: "T", Company: CompanyRef{Slug: "hanko", Name: "Hanko"}, Active: "tasks", Crumbs: []Crumb{{Label: "Hanko", Href: "/c/hanko/"}, {Label: "Tasks"}}, Version: "t", Owner: "Você"}

// Every page must render from zero-value view models (empty states) without error.
func TestRenderZeroValues(t *testing.T) {
	pages := map[string]templ.Component{
		"dashboard":  DashboardPage(sh, DashboardVM{}),
		"tasks":      TasksPage(sh, TasksVM{}),
		"task":       TaskPage(sh, TaskVM{}, "/c/hanko"),
		"task-side":  TaskSideUpdate(TaskVM{}, "/c/hanko"),
		"thread":     Thread(TaskVM{}, "/c/hanko"),
		"new-task":   NewTaskPage(sh, NewTaskVM{}),
		"personas":   PersonasPage(sh, PersonasVM{}),
		"hire":       HirePersonaPage(sh, HirePersonaVM{}),
		"persona":    PersonaPage(sh, PersonaVM{}),
		"runs":       RunsPage(sh, RunsVM{}),
		"run":        RunPage(sh, RunVM{}),
		"run-live":   RunPage(sh, RunVM{SSEHref: "/x/sse", Active: true, CancelHref: "/x/cancelar"}),
		"run-line":   RunLine(LogLine{Kind: "assistant"}),
		"activity":   ActivityPage(sh, ActivityVM{}),
		"onboarding": OnboardingPage(OnboardingVM{}, "t"),
		"error":      ErrorPage(sh, ErrorVM{}),
		"error-bare": ErrorBare(ErrorVM{Back: "/"}, "t"),
		"error-frag": ErrorFragment(ErrorVM{}),
		"soon":       SoonPage(sh, SoonVM{}),
		"mark":       Mark(22),
	}
	for name, c := range pages {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := c.Render(context.Background(), &buf); err != nil {
				t.Fatal(err)
			}
			if buf.Len() == 0 {
				t.Fatal("saída vazia")
			}
		})
	}
}

func TestEveryIconDraws(t *testing.T) {
	names := []string{}
	for _, g := range Nav {
		for _, it := range g.Items {
			names = append(names, it.Icon)
		}
	}
	names = append(names, "panel-left", "plus", "chevron-right", "chevron-down", "settings", "alert", "arrow-up", "square", "play", "check", "x", "user", "lock", "menu", "send", "zap", "bot")
	for _, n := range names {
		var buf bytes.Buffer
		if err := Icon(n).Render(context.Background(), &buf); err != nil {
			t.Fatal(err)
		}
		// the fallback circle is the only <circle> without other shapes; named icons must not hit it
		if strings.Contains(buf.String(), `r="9"`) {
			t.Errorf("ícone %q caiu no fallback", n)
		}
	}
}

func TestNavLinks(t *testing.T) {
	for _, g := range Nav {
		for _, it := range g.Items {
			h := it.Href("/c/hanko")
			if it.Milestone != "" && !strings.HasPrefix(h, "/c/hanko/em-breve/") {
				t.Errorf("%s: href %q", it.Key, h)
			}
			if it.Milestone != "" && it.Blurb == "" {
				t.Errorf("%s: sem texto para a página Em breve", it.Key)
			}
		}
	}
	if it, _ := FindNav("painel"); it.Href("/c/x") != "/c/x/" {
		t.Error("painel href")
	}
	if it, _ := FindNav("tasks"); it.Href("/c/x") != "/c/x/tasks" {
		t.Error("tasks href")
	}
}

func TestInitials(t *testing.T) {
	for in, want := range map[string]string{"Ada": "A", "ada lovelace": "AL", "": "?", "  kenji  ": "K", "Maria de Souza": "MS"} {
		if got := Initials(in); got != want {
			t.Errorf("Initials(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTemplEscapesViewModelText(t *testing.T) {
	vm := TaskVM{Key: "HNK-1", Title: `<script>alert(1)</script>`, PersonaName: `"><svg onload=x>`, Thread: []Msg{{Author: "<b>x</b>", Body: "<p>ok</p>"}}}
	var buf bytes.Buffer
	if err := TaskPage(sh, vm, "/c/hanko").Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, bad := range []string{"<script>alert", "<svg onload", "<b>x</b>"} {
		if strings.Contains(out, bad) {
			t.Errorf("saída contém %q sem escape", bad)
		}
	}
	// Body is pre-sanitised HTML by contract and passes through.
	if !strings.Contains(out, "<p>ok</p>") {
		t.Error("Body (HTML sanitizado) deveria passar")
	}
}
