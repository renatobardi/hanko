// Command preview renders every UI page with realistic sample data for visual
// QA. It mounts the real handlers on an in-memory backend (internal/ui/uitest).
//
//	gorun run ./internal/ui/preview -out /tmp/ui-preview     # static HTML snapshot
//	gorun run ./internal/ui/preview -serve 127.0.0.1:7421     # live server (SSE simulated)
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/ui"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

func main() {
	out := flag.String("out", "", "write a static snapshot of every page to this directory")
	serve := flag.String("serve", "", "serve the preview app on this address (e.g. 127.0.0.1:7421)")
	empty := flag.Bool("empty", false, "start from a fresh company with no data")
	none := flag.Bool("none", false, "start without any company (onboarding)")
	flag.Parse()
	if *out == "" && *serve == "" {
		flag.Usage()
		os.Exit(2)
	}

	var fake *uitest.Fake
	switch {
	case *none:
		fake = uitest.None()
	case *empty:
		fake = uitest.Empty()
	default:
		fake = uitest.Sample()
	}
	broker := events.NewBroker()
	mux := http.NewServeMux()
	ui.MountBackend(mux, fake, ui.Deps{Broker: broker, Version: "preview"})

	if *serve != "" {
		go simulate(fake, broker)
		log.Printf("preview em http://%s/", *serve)
		log.Fatal(http.ListenAndServe(*serve, mux))
	}
	if err := snapshot(mux, fake, *out); err != nil {
		log.Fatal(err)
	}
}

// simulate appends a log line to the sample live run every few seconds.
func simulate(f *uitest.Fake, b *events.Broker) {
	run := f.Runs[0].ID
	lines := []struct {
		k domain.RunEventKind
		t string
	}{
		{domain.EventToolUse, "Read docs/piloto-set-2026.md"},
		{domain.EventAssistant, "Dados do piloto lidos; seguindo para o slide 2."},
		{domain.EventToolUse, "Write work/slide-02-contexto.md (v1)"},
		{domain.EventStdout, "slide-02-contexto.md escrito (388 palavras)"},
	}
	for i := 0; ; i++ {
		time.Sleep(3 * time.Second)
		l := lines[i%len(lines)]
		e := f.AppendEvent(run, l.k, l.t)
		b.Publish(events.Message{Topic: events.RunTopic(run.String()), Type: "run.event", Data: e})
	}
}

var abs = regexp.MustCompile(`(href|src)="/static/`)

func snapshot(h http.Handler, f *uitest.Fake, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	co := f.Companies[0].Slug
	r0 := f.Runs[0].ID.String()
	pages := map[string]string{
		"painel":        "/c/" + co + "/",
		"tasks":         "/c/" + co + "/tasks",
		"tasks-backlog": "/c/" + co + "/tasks?aba=backlog",
		"task":          "/c/" + co + "/tasks/HNK-221",
		"task-nova":     "/c/" + co + "/tasks/nova",
		"personas":      "/c/" + co + "/personas",
		"persona-nova":  "/c/" + co + "/personas/nova",
		"persona":       "/c/" + co + "/personas/ada",
		"execucoes":     "/c/" + co + "/execucoes",
		"execucao":      "/c/" + co + "/execucoes/" + r0,
		"atividade":     "/c/" + co + "/atividade",
		"em-breve":      "/c/" + co + "/em-breve/gates",
		"custos":        "/c/" + co + "/custos",
		"fluxos":        "/c/" + co + "/fluxos",
		"fluxo":         "/c/" + co + "/fluxos/digest-semanal",
		"fluxo-novo":    "/c/" + co + "/fluxos/novo",
		"bem-vindo":     "/bem-vindo",
	}
	for name, path := range pages {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			return fmt.Errorf("%s: status %d", path, rec.Code)
		}
		body, _ := io.ReadAll(rec.Body)
		html := abs.ReplaceAllString(string(body), `$1="static/`)
		if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(html), 0o644); err != nil {
			return err
		}
	}
	// static assets next to the pages
	for _, a := range []string{"hanko.css", "hanko.js", "favicon.svg", "vendor/htmx.min.js", "vendor/sse.min.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/"+a, nil))
		if rec.Code != http.StatusOK {
			return fmt.Errorf("static %s: %d", a, rec.Code)
		}
		dst := filepath.Join(dir, "static", a)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, rec.Body.Bytes(), 0o644); err != nil {
			return err
		}
	}
	fmt.Println("snapshot em", dir)
	return nil
}
