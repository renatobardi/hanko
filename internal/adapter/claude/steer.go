package claude

import (
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
)

// steerGrace é quanto o claude pode ficar em silêncio, depois de um result, com mensagens ainda sem result, antes de
// o Hanko fechar o stdin (uma mensagem injetada no meio de um turno pode ter sido absorvida por ele, e sem esse limite
// a run esperaria o tempo limite inteiro).
const steerGrace = 20 * time.Second

// errStdinClosed: a sessão já está terminando; a mensagem fica na fila de follow-ups para a run seguinte.
var errStdinClosed = errors.New("claude: a sessão já está encerrando")

// steering diz se a run usa o stdin interativo (stream-json) para receber mensagens do operador.
func steering(spec adapter.Spec) bool { return spec.Steer != nil && !spec.ReadOnly }

// userMessage é uma linha de entrada do `claude -p --input-format stream-json`.
type userMessage struct {
	Type    string `json:"type"`
	Message struct {
		Role    string        `json:"role"`
		Content []contentText `json:"content"`
	} `json:"message"`
}

type contentText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// EncodeUserMessage codifica um turno do usuário (o prompt da run ou uma mensagem injetada) como uma linha JSON,
// terminada em quebra de linha, no formato que o claude lê do stdin em stream-json.
func EncodeUserMessage(text string) ([]byte, error) {
	var m userMessage
	m.Type = "user"
	m.Message.Role = "user"
	m.Message.Content = []contentText{{Type: "text", Text: text}}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// feeder escreve as mensagens no stdin do claude e o fecha quando todo turno enviado já devolveu seu result.
type feeder struct {
	grace time.Duration
	ready chan struct{} // fechado quando o stdin existe (ou a sessão acabou antes disso)
	once  sync.Once

	mu     sync.Mutex
	w      io.WriteCloser
	sent   int
	done   int
	closed bool
	timer  *time.Timer
}

func newFeeder(grace time.Duration) *feeder { return &feeder{grace: grace, ready: make(chan struct{})} }

// attach liga o stdin e escreve antes de tudo o prompt da run (nenhuma mensagem injetada passa na frente dele).
func (f *feeder) attach(w io.WriteCloser, prompt string) error {
	b, err := EncodeUserMessage(prompt)
	f.mu.Lock()
	defer f.mu.Unlock()
	defer f.once.Do(func() { close(f.ready) })
	f.w = w
	if f.closed {
		_ = w.Close()
		return errStdinClosed
	}
	if err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	f.sent++
	return nil
}

// send escreve uma mensagem; errStdinClosed se a sessão já está encerrando.
func (f *feeder) send(text string) error {
	b, err := EncodeUserMessage(text)
	if err != nil {
		return err
	}
	<-f.ready // a mensagem pode chegar antes de o processo subir
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || f.w == nil {
		return errStdinClosed
	}
	if _, err := f.w.Write(b); err != nil {
		return err
	}
	f.sent++
	if f.timer != nil {
		f.timer.Stop()
		f.timer = nil
	}
	return nil
}

// observe é chamado a cada linha de saída com o total de results vistos: fecha o stdin quando não há turno
// pendente; se há, rearma o limite de silêncio.
func (f *feeder) observe(results int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	if results > f.done {
		f.done = results
	}
	switch {
	case f.sent > 0 && f.done >= f.sent:
		f.closeLocked()
	case f.done > 0 && f.done < f.sent: // já houve result, mas ainda há mensagem sem resposta
		if f.timer != nil {
			f.timer.Reset(f.grace)
		} else {
			f.timer = time.AfterFunc(f.grace, f.close)
		}
	}
}

func (f *feeder) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeLocked()
}

func (f *feeder) closeLocked() {
	if f.closed {
		return
	}
	f.closed = true
	f.once.Do(func() { close(f.ready) })
	if f.timer != nil {
		f.timer.Stop()
	}
	if f.w != nil {
		_ = f.w.Close()
	}
}
