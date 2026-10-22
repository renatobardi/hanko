package channel

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
)

// Telegram é o driver da Bot API do Telegram. A entrada chega por webhook (setWebhook com secret_token); só conversas
// privadas valem. Texto sem formatação (parse_mode vazio): o que a persona escreve nunca vira marcação.
type Telegram struct{}

// DefaultTelegramBase é a raiz da Bot API.
const DefaultTelegramBase = "https://api.telegram.org"

// Limites da Bot API.
const (
	telegramMaxText     = 4096
	telegramChunk       = 4000
	telegramMaxCallback = 64
	telegramSecretHdr   = "X-Telegram-Bot-Api-Secret-Token"
)

// Kind implementa Driver.
func (Telegram) Kind() domain.ChannelKind { return domain.ChannelTelegram }

func (Telegram) base(c Client) string {
	if c.Config.APIBase != "" {
		return c.Config.APIBase
	}
	return DefaultTelegramBase
}

func (t Telegram) method(c Client, m string) string {
	return t.base(c) + "/bot" + c.Token + "/" + m
}

// Verify compara o cabeçalho secret_token em tempo constante.
func (Telegram) Verify(h http.Header, _ []byte, secret string) error {
	got := h.Get(telegramSecretHdr)
	if secret == "" || got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
		return ErrUnauthorized
	}
	return nil
}

type tgUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

type tgChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type tgPhoto struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type tgDocument struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

type tgMessage struct {
	MessageID int64       `json:"message_id"`
	From      *tgUser     `json:"from"`
	Chat      tgChat      `json:"chat"`
	Text      string      `json:"text"`
	Caption   string      `json:"caption"`
	Photo     []tgPhoto   `json:"photo"`
	Document  *tgDocument `json:"document"`
}

type tgUpdate struct {
	UpdateID      int64      `json:"update_id"`
	Message       *tgMessage `json:"message"`
	CallbackQuery *struct {
		ID      string     `json:"id"`
		From    tgUser     `json:"from"`
		Message *tgMessage `json:"message"`
		Data    string     `json:"data"`
	} `json:"callback_query"`
}

func (u tgUser) label() string {
	if u.Username != "" {
		return "@" + u.Username
	}
	return u.FirstName
}

// Parse normaliza um update.
func (Telegram) Parse(_ http.Header, body []byte, _ domain.ChannelConfig) ([]Inbound, error) {
	var up tgUpdate
	if err := json.Unmarshal(body, &up); err != nil {
		return nil, errors.New("telegram: corpo não é um update válido")
	}
	id := strconv.FormatInt(up.UpdateID, 10)
	ignore := func(reason string) []Inbound {
		return []Inbound{{ExternalID: id, Kind: InIgnore, Reason: reason}}
	}
	switch {
	case up.CallbackQuery != nil:
		cq := up.CallbackQuery
		if cq.Message == nil || cq.Message.Chat.Type != "private" || cq.From.IsBot {
			return ignore("botão fora de uma conversa privada"), nil
		}
		return []Inbound{{ExternalID: id, Kind: InAction, Peer: strconv.FormatInt(cq.Message.Chat.ID, 10),
			Sender: strconv.FormatInt(cq.From.ID, 10), SenderLabel: cq.From.label(), Action: cq.Data, CallbackID: cq.ID}}, nil
	case up.Message != nil:
		m := up.Message
		if m.Chat.Type != "private" {
			return ignore("só conversas privadas valem"), nil
		}
		if m.From == nil || m.From.IsBot {
			return ignore("remetente é um bot"), nil
		}
		in := Inbound{ExternalID: id, Kind: InText, Peer: strconv.FormatInt(m.Chat.ID, 10), Sender: strconv.FormatInt(m.From.ID, 10),
			SenderLabel: m.From.label(), Text: strings.TrimSpace(m.Text)}
		if in.Text == "" {
			in.Text = strings.TrimSpace(m.Caption)
		}
		if m.Document != nil {
			name := m.Document.FileName
			if name == "" {
				name = "documento-" + id
			}
			in.Attachments = append(in.Attachments, Attachment{Ref: m.Document.FileID, Name: name, Mime: m.Document.MimeType, Size: m.Document.FileSize})
		}
		if len(m.Photo) > 0 {
			best := m.Photo[0]
			for _, p := range m.Photo {
				if p.Width*p.Height > best.Width*best.Height {
					best = p
				}
			}
			in.Attachments = append(in.Attachments, Attachment{Ref: best.FileID, Name: "foto-" + id + ".jpg", Mime: "image/jpeg", Size: best.FileSize})
		}
		if strings.HasPrefix(in.Text, "/") {
			first, rest, _ := strings.Cut(in.Text, " ")
			cmd := strings.ToLower(strings.TrimPrefix(first, "/"))
			if i := strings.Index(cmd, "@"); i >= 0 { // /vincular@meu_bot
				cmd = cmd[:i]
			}
			in.Kind, in.Command, in.Arg = InCommand, cmd, strings.TrimSpace(rest)
		}
		if in.Text == "" && len(in.Attachments) == 0 {
			return ignore("mensagem sem texto nem anexo"), nil
		}
		return []Inbound{in}, nil
	}
	return ignore("tipo de update não tratado"), nil
}

type tgResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

// api chama um método JSON e devolve o result. Falha do provedor vira erro sem o token.
func (t Telegram) api(ctx context.Context, c Client, method string, body any) (json.RawMessage, error) {
	status, b, err := call(ctx, c.HTTP, http.MethodPost, t.method(c, method), nil, body)
	if err != nil {
		return nil, err
	}
	return t.result(c, method, status, b)
}

func (Telegram) result(c Client, method string, status int, b []byte) (json.RawMessage, error) {
	var r tgResponse
	_ = json.Unmarshal(b, &r)
	if status == http.StatusOK && r.OK {
		return r.Result, nil
	}
	err := classify(status, "telegram "+method)
	if r.Description != "" {
		err = fmt.Errorf("%w (%s)", err, clip(scrub(r.Description, c.Token), 200)) // %w mantém a classificação permanente
	}
	return nil, err
}

// splitText parte o texto em pedaços que cabem numa mensagem, de preferência em quebras de linha.
func splitText(s string) []string {
	r := []rune(s)
	if len(r) <= telegramMaxText {
		return []string{s}
	}
	var out []string
	for len(r) > telegramMaxText {
		cut := telegramChunk
		for i := cut; i > cut/2; i-- {
			if r[i] == '\n' {
				cut = i
				break
			}
		}
		out = append(out, strings.TrimRight(string(r[:cut]), "\n "))
		r = []rune(strings.TrimLeft(string(r[cut:]), "\n "))
	}
	if len(r) > 0 {
		out = append(out, string(r))
	}
	return out
}

// Send entrega o texto (em pedaços, com os botões no último) e os documentos; devolve o id da última mensagem.
func (t Telegram) Send(ctx context.Context, c Client, out Outbound) (string, error) {
	if _, err := strconv.ParseInt(out.Peer, 10, 64); err != nil {
		return "", &PermanentError{Err: errors.New("telegram: destino inválido")}
	}
	var last string
	chunks := splitText(strings.TrimSpace(out.Text))
	if strings.TrimSpace(out.Text) == "" {
		chunks = nil
	}
	for i, ch := range chunks {
		body := map[string]any{"chat_id": out.Peer, "text": ch, "disable_web_page_preview": true}
		if i == len(chunks)-1 && len(out.Buttons) > 0 {
			kb := make([][]map[string]string, 0, len(out.Buttons))
			for _, row := range out.Buttons {
				var r []map[string]string
				for _, b := range row {
					switch {
					case b.URL != "":
						r = append(r, map[string]string{"text": b.Text, "url": b.URL})
					case b.Data != "" && len(b.Data) <= telegramMaxCallback:
						r = append(r, map[string]string{"text": b.Text, "callback_data": b.Data})
					default:
						return "", &PermanentError{Err: errors.New("telegram: botão inválido (dados acima de 64 bytes)")}
					}
				}
				kb = append(kb, r)
			}
			body["reply_markup"] = map[string]any{"inline_keyboard": kb}
		}
		res, err := t.api(ctx, c, "sendMessage", body)
		if err != nil {
			return "", err
		}
		last = messageID(res)
	}
	for _, d := range out.Documents {
		res, err := t.sendDocument(ctx, c, out.Peer, d)
		if err != nil {
			return "", err
		}
		last = messageID(res)
	}
	if last == "" && len(chunks) == 0 && len(out.Documents) == 0 {
		return "", &PermanentError{Err: errors.New("telegram: mensagem vazia")}
	}
	return last, nil
}

func messageID(res json.RawMessage) string {
	var m struct {
		MessageID int64 `json:"message_id"`
	}
	_ = json.Unmarshal(res, &m)
	if m.MessageID == 0 {
		return ""
	}
	return strconv.FormatInt(m.MessageID, 10)
}

func (t Telegram) sendDocument(ctx context.Context, c Client, peer string, d Document) (json.RawMessage, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("chat_id", peer)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="document"; filename=%q`, strings.NewReplacer("\"", "", "\r", "", "\n", "").Replace(d.Name)))
	mt := d.Mime
	if mt == "" {
		mt = "application/octet-stream"
	}
	h.Set("Content-Type", mt)
	part, err := mw.CreatePart(h)
	if err != nil {
		return nil, &PermanentError{Err: errors.New("telegram: montar documento")}
	}
	if _, err := part.Write(d.Content); err != nil {
		return nil, &PermanentError{Err: errors.New("telegram: montar documento")}
	}
	if err := mw.Close(); err != nil {
		return nil, &PermanentError{Err: errors.New("telegram: montar documento")}
	}
	status, b, err := send(ctx, c.HTTP, http.MethodPost, t.method(c, "sendDocument"), nil, &buf, mw.FormDataContentType())
	if err != nil {
		return nil, err
	}
	return t.result(c, "sendDocument", status, b)
}

// Fetch baixa um arquivo: getFile devolve o caminho e o conteúdo vem de /file/bot<token>/<caminho>.
func (t Telegram) Fetch(ctx context.Context, c Client, ref string, max int64) ([]byte, error) {
	res, err := t.api(ctx, c, "getFile", map[string]string{"file_id": ref})
	if err != nil {
		return nil, err
	}
	var f struct {
		FilePath string `json:"file_path"`
		FileSize int64  `json:"file_size"`
	}
	if json.Unmarshal(res, &f) != nil || f.FilePath == "" {
		return nil, &PermanentError{Err: errors.New("telegram: o arquivo não está disponível")}
	}
	if max > 0 && f.FileSize > max {
		return nil, &PermanentError{Err: fmt.Errorf("telegram: o arquivo tem %d bytes e o limite é %d", f.FileSize, max)}
	}
	u := t.base(c) + "/file/bot" + c.Token + "/" + (&url.URL{Path: f.FilePath}).EscapedPath()
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, &PermanentError{Err: errors.New("telegram: endereço do arquivo inválido")}
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("canal: falha de rede ao baixar o arquivo")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, classify(resp.StatusCode, "telegram download")
	}
	limit := max
	if limit <= 0 {
		limit = 20 << 20
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, errors.New("canal: falha ao ler o arquivo")
	}
	if int64(len(b)) > limit {
		return nil, &PermanentError{Err: fmt.Errorf("telegram: o arquivo passa do limite de %d bytes", limit)}
	}
	return b, nil
}

// Register liga o webhook do bot a url, autenticado por secret.
func (t Telegram) Register(ctx context.Context, c Client, hookURL, secret string) error {
	_, err := t.api(ctx, c, "setWebhook", map[string]any{"url": hookURL, "secret_token": secret,
		"allowed_updates": []string{"message", "callback_query"}})
	return err
}

// Ack responde ao clique (answerCallbackQuery).
func (t Telegram) Ack(ctx context.Context, c Client, callbackID, text string) error {
	if callbackID == "" {
		return nil
	}
	body := map[string]any{"callback_query_id": callbackID}
	if text != "" {
		body["text"] = clip(text, 190)
	}
	_, err := t.api(ctx, c, "answerCallbackQuery", body)
	return err
}
