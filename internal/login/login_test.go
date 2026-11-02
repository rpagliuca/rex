package login

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const clienteDeTeste = "cliente.apps.googleusercontent.com"

func novoDeTeste(t *testing.T) *Login {
	t.Helper()
	ambiente := map[string]string{"SEGREDO": "s", "CHAVE": strings.Repeat("k", 32)}
	l, err := Novo(Config{
		URLBase:              "https://rex.exemplo.ts.net/",
		ClientID:             clienteDeTeste,
		ClientSecretAmbiente: "SEGREDO",
		ChaveAmbiente:        "CHAVE",
		Emails:               []string{"Dono@Exemplo.com"},
	}, func(k string) string { return ambiente[k] })
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func idToken(t *testing.T, afirmacoes map[string]any) string {
	t.Helper()
	corpo, err := json.Marshal(afirmacoes)
	if err != nil {
		t.Fatal(err)
	}
	return "e30." + base64.RawURLEncoding.EncodeToString(corpo) + ".assinatura"
}

func afirmacoes(email string) map[string]any {
	return map[string]any{
		"iss": "https://accounts.google.com", "aud": clienteDeTeste, "exp": time.Now().Add(time.Hour).Unix(),
		"email": email, "email_verified": true,
	}
}

var protegido = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("dentro")) })

// entrarComo percorre o fluxo inteiro e devolve os cookies da resposta do retorno.
func entrarComo(t *testing.T, l *Login, token string) *httptest.ResponseRecorder {
	t.Helper()
	l.trocar = func(_ context.Context, codigo, verificador string) (string, error) {
		if codigo != "codigo" || verificador == "" {
			t.Errorf("troca com código %q e verificador %q", codigo, verificador)
		}
		return token, nil
	}
	h := l.Proteger(protegido)
	ida := httptest.NewRecorder()
	h.ServeHTTP(ida, httptest.NewRequest("GET", CaminhoEntrar, nil))
	destino, err := url.Parse(ida.Header().Get("Location"))
	if err != nil || destino.Host != "accounts.google.com" {
		t.Fatalf("entrar devia ir ao Google, foi a %q", ida.Header().Get("Location"))
	}
	if destino.Query().Get("redirect_uri") != "https://rex.exemplo.ts.net/login/retorno" || destino.Query().Get("code_challenge") == "" {
		t.Fatalf("pedido ao Google incompleto: %s", destino)
	}
	volta := httptest.NewRequest("GET", CaminhoRetorno+"?code=codigo&state="+destino.Query().Get("state"), nil)
	for _, c := range ida.Result().Cookies() {
		volta.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, volta)
	return w
}

func comCookies(r *http.Request, w *httptest.ResponseRecorder) *http.Request {
	for _, c := range w.Result().Cookies() {
		if c.MaxAge >= 0 {
			r.AddCookie(c)
		}
	}
	return r
}

func TestSemLoginNavegacaoVaiAoGoogleEApiRecebe401(t *testing.T) {
	h := novoDeTeste(t).Proteger(protegido)

	pagina := httptest.NewRequest("GET", "/", nil)
	pagina.Header.Set("Accept", "text/html,application/xhtml+xml")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, pagina)
	if w.Code != http.StatusFound || w.Header().Get("Location") != CaminhoEntrar {
		t.Fatalf("página sem login: %d %q", w.Code, w.Header().Get("Location"))
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/tarefas", nil))
	if w.Code != http.StatusUnauthorized || w.Header().Get("X-Rex-Login") != "necessario" {
		t.Fatalf("API sem login: %d", w.Code)
	}
}

func TestEmailLiberadoEntra(t *testing.T) {
	l := novoDeTeste(t)
	w := entrarComo(t, l, idToken(t, afirmacoes("dono@exemplo.com")))
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/" {
		t.Fatalf("retorno: %d %s", w.Code, w.Body)
	}
	r := httptest.NewRecorder()
	l.Proteger(protegido).ServeHTTP(r, comCookies(httptest.NewRequest("GET", "/tarefas", nil), w))
	if r.Body.String() != "dentro" {
		t.Fatalf("com o cookie: %d %s", r.Code, r.Body)
	}
}

func TestRecusas(t *testing.T) {
	casos := map[string]func(map[string]any){
		"outro e-mail":          func(a map[string]any) { a["email"] = "intruso@exemplo.com" },
		"e-mail não verificado": func(a map[string]any) { a["email_verified"] = false },
		"outro cliente":         func(a map[string]any) { a["aud"] = "outro" },
		"outro emissor":         func(a map[string]any) { a["iss"] = "https://malicioso" },
		"expirado":              func(a map[string]any) { a["exp"] = time.Now().Add(-time.Minute).Unix() },
	}
	for nome, alterar := range casos {
		t.Run(nome, func(t *testing.T) {
			a := afirmacoes("dono@exemplo.com")
			alterar(a)
			l := novoDeTeste(t)
			w := entrarComo(t, l, idToken(t, a))
			if w.Code != http.StatusForbidden {
				t.Fatalf("devia recusar: %d %s", w.Code, w.Body)
			}
			r := httptest.NewRecorder()
			l.Proteger(protegido).ServeHTTP(r, comCookies(httptest.NewRequest("GET", "/tarefas", nil), w))
			if r.Code != http.StatusUnauthorized {
				t.Fatalf("recusado não pode ganhar cookie: %d", r.Code)
			}
		})
	}
}

func TestRetornoSemFluxoOuComStateErradoERecusado(t *testing.T) {
	l := novoDeTeste(t)
	l.trocar = func(context.Context, string, string) (string, error) {
		t.Fatal("não podia trocar o código")
		return "", nil
	}
	w := httptest.NewRecorder()
	l.Proteger(protegido).ServeHTTP(w, httptest.NewRequest("GET", CaminhoRetorno+"?code=codigo&state=qualquer", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("sem cookie de fluxo: %d", w.Code)
	}
}

func TestCookieAdulteradoOuVencidoNaoVale(t *testing.T) {
	l := novoDeTeste(t)
	w := entrarComo(t, l, idToken(t, afirmacoes("dono@exemplo.com")))
	var sessao *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieSessao {
			sessao = c
		}
	}

	adulterado := *sessao
	adulterado.Value = base64.RawURLEncoding.EncodeToString([]byte("intruso@exemplo.com|9999999999")) + sessao.Value[strings.Index(sessao.Value, "."):]
	r := httptest.NewRequest("GET", "/tarefas", nil)
	r.AddCookie(&adulterado)
	if _, ok := l.emailDoCookie(r); ok {
		t.Fatal("cookie adulterado aceito")
	}

	r = httptest.NewRequest("GET", "/tarefas", nil)
	r.AddCookie(sessao)
	l.agora = func() time.Time { return time.Now().Add(duracaoPadrao + time.Hour) }
	if _, ok := l.emailDoCookie(r); ok {
		t.Fatal("cookie vencido aceito")
	}
}

func TestSairApagaOCookie(t *testing.T) {
	l := novoDeTeste(t)
	w := httptest.NewRecorder()
	l.Proteger(protegido).ServeHTTP(w, httptest.NewRequest("GET", CaminhoSair, nil))
	c := w.Result().Cookies()
	if len(c) != 1 || c[0].Name != cookieSessao || c[0].MaxAge >= 0 {
		t.Fatalf("sair devia apagar o cookie: %+v", c)
	}
}

func TestConfiguracaoInvalida(t *testing.T) {
	ambiente := func(k string) string {
		return map[string]string{"SEGREDO": "s", "CHAVE": strings.Repeat("k", 32), "CURTA": "k"}[k]
	}
	base := Config{URLBase: "https://x.ts.net", ClientID: "c", ClientSecretAmbiente: "SEGREDO", ChaveAmbiente: "CHAVE", Emails: []string{"a@b"}}
	casos := map[string]func(*Config){
		"http fora de localhost": func(c *Config) { c.URLBase = "http://100.64.0.10:8790" },
		"sem client secret":      func(c *Config) { c.ClientSecretAmbiente = "NADA" },
		"chave curta":            func(c *Config) { c.ChaveAmbiente = "CURTA" },
		"sem e-mails":            func(c *Config) { c.Emails = nil },
	}
	for nome, alterar := range casos {
		c := base
		alterar(&c)
		if _, err := Novo(c, ambiente); err == nil {
			t.Errorf("%s: devia recusar", nome)
		}
	}
	if _, err := Novo(base, ambiente); err != nil {
		t.Fatalf("configuração válida recusada: %v", err)
	}
}
