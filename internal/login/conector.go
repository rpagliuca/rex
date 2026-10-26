package login

// Conector personalizado do Claude: o rex serve o endpoint MCP remoto em /mcp e é o próprio
// servidor de autorização OAuth 2.1 dele (o que o claude.ai exige de um conector personalizado):
// metadados (RFC 9728 e RFC 8414), registro dinâmico de cliente (RFC 7591), código com PKCE S256 e
// refresh com rotação.
//
// Nada fica no banco e não há segredo novo: o cliente registrado, o código, o access token e o
// refresh token são valores assinados com HMAC pela chave dos cookies, cada tipo com o seu nome na
// assinatura. Quem autoriza é o login pelo Google de sempre, e tirar um e-mail da configuração
// desfaz também os tokens dele. Não há tela de consentimento: a autorização só devolve o código a um
// redirect_uri do Claude (claude.ai, claude.com) ou de loopback (Claude Code).

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// Caminhos do conector; ficam livres do cookie de login.
const (
	CaminhoMCP                = "/mcp"
	caminhoRecursoProtegido   = "/.well-known/oauth-protected-resource"
	caminhoServidorAutorizado = "/.well-known/oauth-authorization-server"
	caminhoRegistrar          = "/oauth/registrar"
	caminhoAutorizar          = "/oauth/autorizar"
	caminhoToken              = "/oauth/token"
)

const (
	duracaoCodigo = time.Minute
	duracaoAcesso = time.Hour
	// Nomes que entram na assinatura: o valor de um tipo não serve como outro.
	seloCliente = "rex_oauth_cliente"
	seloCodigo  = "rex_oauth_codigo"
	seloAcesso  = "rex_oauth_acesso"
	seloRefresh = "rex_oauth_refresh"
)

// redirectsLiberados são os callbacks de conector personalizado do Claude.
var redirectsLiberados = []string{
	"https://claude.ai/api/mcp/auth_callback",
	"https://claude.com/api/mcp/auth_callback",
}

// conector guarda o handler MCP e os códigos e refresh tokens já trocados, até expirarem: cada um
// vale uma vez. A memória se perde num reinício, e aí um refresh já usado volta a valer até expirar.
type conector struct {
	mcp    http.Handler
	mu     sync.Mutex
	usados map[string]int64
}

// ComConector serve h em /mcp, atrás de OAuth, nos endereços com login.
func (l *Login) ComConector(h http.Handler) {
	l.conector = &conector{mcp: h, usados: map[string]int64{}}
}

func (l *Login) rotasDoConector(mux *http.ServeMux) {
	recurso := l.base + CaminhoMCP
	metadados := l.base + caminhoRecursoProtegido + CaminhoMCP
	protegido := auth.RequireBearerToken(l.verificarAcesso, &auth.RequireBearerTokenOptions{ResourceMetadataURL: metadados})
	mux.Handle(CaminhoMCP, registrar(protegido(l.conector.mcp)))
	recursoProtegido := func(w http.ResponseWriter, _ *http.Request) {
		responderJSON(w, http.StatusOK, map[string]any{
			"resource":                 recurso,
			"authorization_servers":    []string{l.base},
			"bearer_methods_supported": []string{"header"},
			"resource_name":            "rex",
		})
	}
	mux.Handle("GET "+caminhoRecursoProtegido, registrar(http.HandlerFunc(recursoProtegido)))
	mux.Handle("GET "+caminhoRecursoProtegido+CaminhoMCP, registrar(http.HandlerFunc(recursoProtegido)))
	mux.Handle("GET "+caminhoServidorAutorizado, registrar(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		responderJSON(w, http.StatusOK, map[string]any{
			"issuer":                                         l.base,
			"authorization_endpoint":                         l.base + caminhoAutorizar,
			"token_endpoint":                                 l.base + caminhoToken,
			"registration_endpoint":                          l.base + caminhoRegistrar,
			"response_types_supported":                       []string{"code"},
			"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported":               []string{"S256"},
			"token_endpoint_auth_methods_supported":          []string{"none"},
			"authorization_response_iss_parameter_supported": true,
		})
	})))
	mux.Handle("POST "+caminhoRegistrar, registrar(http.HandlerFunc(l.registrar)))
	mux.Handle("GET "+caminhoAutorizar, registrar(http.HandlerFunc(l.autorizar)))
	mux.Handle("POST "+caminhoToken, registrar(http.HandlerFunc(l.emitirToken)))
}

// registrar loga cada chamada do conector (método, caminho, status, duração e, nos erros, o motivo),
// para diagnosticar o que o Claude recebeu. Nunca loga tokens nem códigos.
func registrar(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inicio := time.Now()
		metodo := metodoMCP(r)
		e := &escritorRegistrado{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(e, r)
		log.Printf("conector: %s %s%s → %d em %s (%s)%s", r.Method, r.URL.Path, metodo, e.status,
			time.Since(inicio).Round(time.Millisecond), r.UserAgent(), e.motivo)
	})
}

// metodoMCP lê do corpo de um POST o método JSON-RPC e, numa chamada de ferramenta, o nome dela
// (" tools/call voz_panorama"), devolvendo o corpo intacto para o handler. Sem parâmetros: eles
// podem trazer o que o Rafael disse.
func metodoMCP(r *http.Request) string {
	if r.Method != http.MethodPost || r.Body == nil {
		return ""
	}
	corpo, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(corpo), r.Body))
	if err != nil {
		return ""
	}
	var m struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if json.Unmarshal(corpo, &m) != nil || m.Method == "" {
		return ""
	}
	if m.Params.Name != "" && m.Method == "tools/call" {
		return " " + m.Method + " " + m.Params.Name
	}
	return " " + m.Method
}

type escritorRegistrado struct {
	http.ResponseWriter
	status int
	motivo string
}

func (e *escritorRegistrado) WriteHeader(status int) {
	e.status = status
	e.ResponseWriter.WriteHeader(status)
}

func (e *escritorRegistrado) Write(b []byte) (int, error) {
	if e.status >= 400 && e.motivo == "" {
		e.motivo = ": " + strings.TrimSpace(string(b))
	}
	return e.ResponseWriter.Write(b)
}

func (e *escritorRegistrado) Flush() {
	if f, ok := e.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// redirectLiberado aceita os callbacks do Claude e loopback em http (RFC 8252, 7.3).
func redirectLiberado(uri string) bool {
	if slices.Contains(redirectsLiberados, uri) {
		return true
	}
	u, err := url.Parse(uri)
	return err == nil && u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")
}

// O cliente registrado é a lista de redirect_uris dele, assinada: o client_id a carrega.

type cliente struct {
	Redirects []string `json:"r"`
}

func (l *Login) registrar(w http.ResponseWriter, r *http.Request) {
	var pedido struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&pedido); err != nil {
		erroOAuth(w, http.StatusBadRequest, "invalid_client_metadata", "corpo inválido")
		return
	}
	if len(pedido.RedirectURIs) == 0 {
		erroOAuth(w, http.StatusBadRequest, "invalid_redirect_uri", "faltam redirect_uris")
		return
	}
	for _, uri := range pedido.RedirectURIs {
		if !redirectLiberado(uri) {
			erroOAuth(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri não liberado: "+uri)
			return
		}
	}
	responderJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  l.selar(seloCliente, cliente{Redirects: pedido.RedirectURIs}),
		"client_id_issued_at":        l.agora().Unix(),
		"client_name":                pedido.ClientName,
		"redirect_uris":              pedido.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

type codigo struct {
	Cliente  string `json:"c"`
	Redirect string `json:"r"`
	Desafio  string `json:"d"`
	Email    string `json:"e"`
	Expira   int64  `json:"x"`
	Nonce    string `json:"n"`
}

func (l *Login) autorizar(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var c cliente
	if !l.abrir(seloCliente, q.Get("client_id"), &c) {
		erroOAuth(w, http.StatusBadRequest, "invalid_client", "client_id desconhecido; registre o conector de novo")
		return
	}
	redirect := q.Get("redirect_uri")
	if redirect == "" && len(c.Redirects) == 1 {
		redirect = c.Redirects[0]
	}
	if !slices.Contains(c.Redirects, redirect) {
		erroOAuth(w, http.StatusBadRequest, "invalid_request", "redirect_uri diferente do registrado")
		return
	}
	// Daqui em diante os erros voltam ao cliente pelo redirect_uri, que já é confiável.
	volta, _ := url.Parse(redirect)
	responder := func(params url.Values) {
		params.Set("iss", l.base)
		if s := q.Get("state"); s != "" {
			params.Set("state", s)
		}
		volta.RawQuery = params.Encode()
		http.Redirect(w, r, volta.String(), http.StatusFound)
	}
	switch {
	case q.Get("response_type") != "code":
		responder(url.Values{"error": {"unsupported_response_type"}})
		return
	case q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256":
		responder(url.Values{"error": {"invalid_request"}, "error_description": {"PKCE S256 obrigatório"}})
		return
	case q.Get("resource") != "" && strings.TrimRight(q.Get("resource"), "/") != l.base+CaminhoMCP:
		responder(url.Values{"error": {"invalid_target"}})
		return
	}
	email, ok := l.emailDoCookie(r)
	if !ok {
		// Volta para cá depois do login pelo Google.
		http.Redirect(w, r, CaminhoEntrar+"?"+url.Values{"voltar": {r.URL.RequestURI()}}.Encode(), http.StatusFound)
		return
	}
	cod := l.selar(seloCodigo, codigo{
		Cliente: q.Get("client_id"), Redirect: redirect, Desafio: q.Get("code_challenge"), Email: email,
		Expira: l.agora().Add(duracaoCodigo).Unix(), Nonce: aleatorio(),
	})
	responder(url.Values{"code": {cod}})
}

// O refresh carrega o cliente para não servir a outro; o access só o e-mail.

type credencial struct {
	Cliente string `json:"c,omitempty"`
	Email   string `json:"e"`
	Expira  int64  `json:"x"`
	Nonce   string `json:"n"`
}

func (l *Login) emitirToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		erroOAuth(w, http.StatusBadRequest, "invalid_request", "formulário inválido")
		return
	}
	f := r.PostForm
	// Cliente público pode mandar o client_id no Basic em vez do formulário (RFC 6749, 2.3.1).
	if id, _, ok := r.BasicAuth(); ok && f.Get("client_id") == "" {
		f.Set("client_id", id)
	}
	agora := l.agora().Unix()
	var email string
	switch f.Get("grant_type") {
	case "authorization_code":
		var c codigo
		switch {
		case !l.abrir(seloCodigo, f.Get("code"), &c) || agora > c.Expira:
			erroOAuth(w, http.StatusBadRequest, "invalid_grant", "código inválido ou expirado")
			return
		case c.Cliente != f.Get("client_id") || (f.Get("redirect_uri") != "" && c.Redirect != f.Get("redirect_uri")):
			erroOAuth(w, http.StatusBadRequest, "invalid_grant", "código de outro cliente")
			return
		case desafioS256(f.Get("code_verifier")) != c.Desafio:
			erroOAuth(w, http.StatusBadRequest, "invalid_grant", "code_verifier não confere")
			return
		case !l.conector.usar(c.Nonce, c.Expira):
			erroOAuth(w, http.StatusBadRequest, "invalid_grant", "código já usado")
			return
		}
		email = c.Email
	case "refresh_token":
		var c credencial
		switch {
		case !l.abrir(seloRefresh, f.Get("refresh_token"), &c) || agora > c.Expira:
			erroOAuth(w, http.StatusBadRequest, "invalid_grant", "refresh token inválido ou expirado")
			return
		case f.Get("client_id") != "" && c.Cliente != f.Get("client_id"):
			erroOAuth(w, http.StatusBadRequest, "invalid_grant", "refresh token de outro cliente")
			return
		case !l.conector.usar(c.Nonce, c.Expira):
			erroOAuth(w, http.StatusBadRequest, "invalid_grant", "refresh token já usado")
			return
		}
		email = c.Email
	default:
		erroOAuth(w, http.StatusBadRequest, "unsupported_grant_type", "use authorization_code ou refresh_token")
		return
	}
	if !slices.Contains(l.emails, email) {
		erroOAuth(w, http.StatusBadRequest, "invalid_grant", "o e-mail "+email+" não tem mais acesso a este rex")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	responderJSON(w, http.StatusOK, map[string]any{
		"access_token": l.selar(seloAcesso, credencial{Email: email, Expira: l.agora().Add(duracaoAcesso).Unix(), Nonce: aleatorio()}),
		"token_type":   "Bearer",
		"expires_in":   int(duracaoAcesso.Seconds()),
		"refresh_token": l.selar(seloRefresh, credencial{
			Cliente: f.Get("client_id"), Email: email, Expira: l.agora().Add(l.duracao).Unix(), Nonce: aleatorio(),
		}),
	})
}

func (l *Login) verificarAcesso(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	var c credencial
	if !l.abrir(seloAcesso, token, &c) || !slices.Contains(l.emails, c.Email) {
		return nil, fmt.Errorf("%w: token inválido", auth.ErrInvalidToken)
	}
	return &auth.TokenInfo{UserID: c.Email, Expiration: time.Unix(c.Expira, 0)}, nil
}

// usar marca o nonce como trocado; falso se já tinha sido. Limpa os vencidos de passagem.
func (c *conector) usar(nonce string, expira int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	agora := time.Now().Unix()
	for n, x := range c.usados {
		if agora > x {
			delete(c.usados, n)
		}
	}
	if _, ok := c.usados[nonce]; ok {
		return false
	}
	c.usados[nonce] = expira
	return true
}

func desafioS256(verificador string) string {
	soma := sha256.Sum256([]byte(verificador))
	return base64.RawURLEncoding.EncodeToString(soma[:])
}

// selar devolve v em JSON, base64 e assinado com o nome do tipo; abrir confere e decodifica.

func (l *Login) selar(nome string, v any) string {
	conteudo, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(conteudo) + "." + l.assinar(nome, string(conteudo))
}

func (l *Login) abrir(nome, selado string, v any) bool {
	dados, assinatura, ok := strings.Cut(selado, ".")
	if !ok {
		return false
	}
	conteudo, err := base64.RawURLEncoding.DecodeString(dados)
	if err != nil || !hmac.Equal([]byte(assinatura), []byte(l.assinar(nome, string(conteudo)))) {
		return false
	}
	return json.Unmarshal(conteudo, v) == nil
}

func responderJSON(w http.ResponseWriter, status int, corpo any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(corpo)
}

func erroOAuth(w http.ResponseWriter, status int, codigo, descricao string) {
	responderJSON(w, status, map[string]string{"error": codigo, "error_description": descricao})
}

// voltarLocal aceita só caminhos deste servidor, para o login não virar redirecionador aberto.
func voltarLocal(v string) string {
	if !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") || strings.HasPrefix(v, "/\\") {
		return ""
	}
	return v
}
