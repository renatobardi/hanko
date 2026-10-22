// Package oauthtest é um servidor MCP + servidor de autorização falsos (httptest) para os testes do OAuth 2.1:
// jamais há chamada de rede real. Valida PKCE S256 de verdade.
package oauthtest

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
)

// Server junta recurso protegido (/mcp) e servidor de autorização no mesmo host.
type Server struct {
	*httptest.Server

	mu          sync.Mutex
	NoDCR       bool // sem registration_endpoint
	NoMetadata  bool // sem metadados: o cliente cai nos endpoints padrão
	NoPKCE      bool // anuncia só "plain"
	ExpiresIn   int  // padrão 3600
	Registered  []map[string]any
	codes       map[string]string // code -> challenge
	access      string
	refresh     string
	n           int
	RefreshHits int
	ExchangeHit int
	Revoked     bool // refresh responde invalid_grant
}

// New sobe o servidor falso.
func New() *Server {
	f := &Server{codes: map[string]string{}, ExpiresIn: 3600}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", f.mcp)
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", f.prm)
	mux.HandleFunc("/.well-known/oauth-authorization-server", f.asMeta)
	mux.HandleFunc("/register", f.register)
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "use IssueCode", http.StatusTeapot) })
	mux.HandleFunc("/token", f.token)
	f.Server = httptest.NewServer(mux)
	return f
}

// MCPURL é o endereço do servidor MCP protegido.
func (f *Server) MCPURL() string { return f.URL + "/mcp" }

// AccessToken é o access token vigente (vazio antes da primeira troca).
func (f *Server) AccessToken() string { f.mu.Lock(); defer f.mu.Unlock(); return f.access }

// RotateAccess invalida o access token atual (o MCP passa a responder 401).
func (f *Server) RotateAccess() { f.mu.Lock(); f.access = ""; f.mu.Unlock() }

// IssueCode simula o consentimento do humano: devolve um código ligado ao code_challenge recebido.
func (f *Server) IssueCode(challenge string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	c := "code-" + strconv.Itoa(f.n)
	f.codes[c] = challenge
	return c
}

func (f *Server) mcp(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	want := f.access
	f.mu.Unlock()
	if want == "" || r.Header.Get("Authorization") != "Bearer "+want {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, f.URL))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if len(req.ID) == 0 { // notificação (notifications/initialized): sem corpo
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var result any
	switch req.Method {
	case "tools/list":
		result = map[string]any{"tools": []any{}}
	default: // initialize e o resto
		result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{"name": "fake", "version": "1"}}
	}
	writeJSON(w, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
}

func (f *Server) prm(w http.ResponseWriter, r *http.Request) {
	if f.NoMetadata {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, map[string]any{"resource": f.URL + "/mcp", "authorization_servers": []string{f.URL}, "scopes_supported": []string{"read", "write"}})
}

func (f *Server) asMeta(w http.ResponseWriter, r *http.Request) {
	if f.NoMetadata {
		http.NotFound(w, r)
		return
	}
	m := map[string]any{"issuer": f.URL, "authorization_endpoint": f.URL + "/authorize", "token_endpoint": f.URL + "/token",
		"code_challenge_methods_supported": []string{"S256"}}
	if f.NoPKCE {
		m["code_challenge_methods_supported"] = []string{"plain"}
	}
	if !f.NoDCR {
		m["registration_endpoint"] = f.URL + "/register"
	}
	writeJSON(w, m)
}

func (f *Server) register(w http.ResponseWriter, r *http.Request) {
	if f.NoDCR {
		http.NotFound(w, r)
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.Registered = append(f.Registered, body)
	f.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"client_id": "client-abc"})
}

func (f *Server) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	defer f.mu.Unlock()
	fail := func(code string) {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": code, "error_description": "falha simulada"})
	}
	if r.PostForm.Get("client_id") != "client-abc" {
		fail("invalid_client")
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		f.ExchangeHit++
		ch, ok := f.codes[r.PostForm.Get("code")]
		delete(f.codes, r.PostForm.Get("code"))
		if !ok {
			fail("invalid_grant")
			return
		}
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if ch != base64.RawURLEncoding.EncodeToString(sum[:]) {
			fail("invalid_grant")
			return
		}
		f.n++
		f.access, f.refresh = "at-"+strconv.Itoa(f.n), "rt-"+strconv.Itoa(f.n)
	case "refresh_token":
		f.RefreshHits++
		if f.Revoked || r.PostForm.Get("refresh_token") != f.refresh {
			fail("invalid_grant")
			return
		}
		f.n++
		f.access, f.refresh = "at-"+strconv.Itoa(f.n), "rt-"+strconv.Itoa(f.n)
	default:
		fail("unsupported_grant_type")
		return
	}
	writeJSON(w, map[string]any{"access_token": f.access, "refresh_token": f.refresh, "token_type": "Bearer", "expires_in": f.ExpiresIn, "scope": "read"})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// ParseAuthURL extrai state, code_challenge e redirect_uri da URL de autorização, como o servidor de verdade faria.
func ParseAuthURL(raw string) (state, challenge, redirect string, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", "", err
	}
	q := u.Query()
	return q.Get("state"), q.Get("code_challenge"), q.Get("redirect_uri"), nil
}
