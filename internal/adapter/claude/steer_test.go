package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
)

func TestEncodeUserMessage(t *testing.T) {
	b, err := EncodeUserMessage("mude o foco: \"testes\"\ne depois docs")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(b, []byte("\n")) || bytes.Count(b, []byte("\n")) != 1 {
		t.Fatalf("deve ser exatamente uma linha terminada em \\n: %q", b)
	}
	var got struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "user" || got.Message.Role != "user" || len(got.Message.Content) != 1 ||
		got.Message.Content[0].Type != "text" || got.Message.Content[0].Text != "mude o foco: \"testes\"\ne depois docs" {
		t.Fatalf("mensagem = %+v", got)
	}
}

func TestBuildArgsSteering(t *testing.T) {
	ch := make(chan adapter.Steer)
	got := BuildArgs(adapter.Spec{Steer: ch}, Config{}, "", "")
	want := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits", "--input-format", "stream-json"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatal(diff)
	}
	// sem canal, ou em revisão somente leitura, o stdin segue sendo o prompt simples
	for _, spec := range []adapter.Spec{{}, {Steer: ch, ReadOnly: true}} {
		if strings.Contains(strings.Join(BuildArgs(spec, Config{}, "", ""), " "), "--input-format") {
			t.Fatalf("spec %+v não deveria usar stream-json na entrada", spec)
		}
	}
}

func TestAdapterDeclaresInject(t *testing.T) {
	if adapter.SteerModeOf(New(Options{})) != domain.SteerInject || domain.SteerModeFor(New(Options{}).Name()) != domain.SteerInject {
		t.Fatal("claude deve declarar inject")
	}
}

type pipeBuf struct {
	bytes.Buffer
	closed bool
}

func (p *pipeBuf) Close() error { p.closed = true; return nil }

func TestFeederClosesWhenEveryTurnAnswered(t *testing.T) {
	w := &pipeBuf{}
	f := newFeeder(time.Hour)
	if err := f.attach(w, "prompt"); err != nil {
		t.Fatal(err)
	}
	if err := f.send("steer"); err != nil {
		t.Fatal(err)
	}
	f.observe(1)
	if w.closed {
		t.Fatal("fechou com um turno ainda sem result")
	}
	f.observe(2)
	if !w.closed {
		t.Fatal("deveria fechar quando todo turno devolveu result")
	}
	if err := f.send("tarde"); !errors.Is(err, errStdinClosed) {
		t.Fatalf("send após o fim = %v", err)
	}
	if n := bytes.Count(w.Bytes(), []byte("\n")); n != 2 {
		t.Fatalf("linhas escritas = %d", n)
	}
}

func TestFeederGraceClosesWhenAbsorbed(t *testing.T) {
	w := &pipeBuf{}
	f := newFeeder(20 * time.Millisecond)
	_ = f.attach(w, "a")
	_ = f.send("b")
	f.observe(1) // um result, mas dois turnos enviados: arma o limite de silêncio
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		c := f.closed
		f.mu.Unlock()
		if c {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("o limite de silêncio não fechou o stdin")
}

func newSteerFake(hold string) *Adapter {
	bin, err := filepath.Abs("testdata/fake-claude-steer.sh")
	if err != nil {
		panic(err)
	}
	return New(Options{Bin: bin, Grace: 500 * time.Millisecond, EnvAllow: []string{"FAKE_*"}})
}

// Sem nenhuma mensagem injetada a sessão tem um turno e o processo termina sozinho (o stdin fecha no result).
func TestSteerRunWithoutMessagesEnds(t *testing.T) {
	c := &collector{}
	ch := make(chan adapter.Steer)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := newSteerFake("").Run(ctx, adapter.Spec{RunID: domain.NewID(), Dir: t.TempDir(), Prompt: "faça X", Steer: ch}, c)
	if err != nil || res.IsError || res.Summary != "turno 1 concluido: faça X" || res.SessionRef != "sess-steer" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := findPrefix(c.texts(domain.EventStderr), "ARGS: "); !strings.Contains(got, "--input-format stream-json") {
		t.Fatalf("args = %q", got)
	}
}

// Uma mensagem injetada vira um segundo turno na mesma sessão e muda o resultado final.
func TestSteerInjectsSecondTurn(t *testing.T) {
	t.Setenv("FAKE_STEER_HOLD", "1")
	c := &collector{}
	ch := make(chan adapter.Steer, 1)
	acked := make(chan error, 1)
	ch <- adapter.Steer{ID: "f1", Text: "agora foque nos testes", Ack: func(err error) { acked <- err }}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := newSteerFake("1").Run(ctx, adapter.Spec{RunID: domain.NewID(), Dir: t.TempDir(), Prompt: "faça X", Steer: ch}, c)
	if err != nil || res.IsError {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	select {
	case e := <-acked:
		if e != nil {
			t.Fatalf("ack = %v", e)
		}
	default:
		t.Fatal("a mensagem não foi confirmada")
	}
	if res.Summary != "turno 2 concluido: agora foque nos testes" {
		t.Fatalf("summary = %q", res.Summary)
	}
	lines := 0
	for _, e := range c.texts(domain.EventStderr) {
		if strings.HasPrefix(e, "USERLINE: ") {
			lines++
		}
	}
	if lines != 2 {
		t.Fatalf("linhas de usuário recebidas pelo harness = %d", lines)
	}
}

// Uma mensagem que chega depois que o harness já terminou não é entregue: o ack leva o erro e ela segue na fila.
func TestSteerAfterEndIsRefused(t *testing.T) {
	ch := make(chan adapter.Steer, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := newSteerFake("").Run(ctx, adapter.Spec{RunID: domain.NewID(), Dir: t.TempDir(), Prompt: "x", Steer: ch}, &collector{}); err != nil {
		t.Fatal(err)
	}
	f := newFeeder(time.Hour)
	f.close()
	if err := f.send("tarde"); !errors.Is(err, errStdinClosed) {
		t.Fatalf("err = %v", err)
	}
}

func TestParserInitCarriesSessionRef(t *testing.T) {
	p := newParser(fixedNow)
	evs := p.Feed([]byte(`{"type":"system","subtype":"init","session_id":"abc-1","model":"m"}`))
	if len(evs) != 1 || evs[0].SessionRef != "abc-1" {
		t.Fatalf("evs = %+v", evs)
	}
	evs = p.Feed([]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_9","name":"Bash","input":{"command":"ls"}}]}}`))
	var pl struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if len(evs) != 1 || json.Unmarshal(evs[0].Payload, &pl) != nil || pl.ID != "toolu_9" || pl.Name != "Bash" {
		t.Fatalf("tool_use sem id no payload: %+v", evs)
	}
}
