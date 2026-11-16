package login

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestIntegracaoSoNosLembretes: o token de integração entra sem cookie só para ler, alterar, adiar e
// confirmar um lembrete; põe o nome dela como autor; token errado é 401, outra rota é 403; e ninguém
// de fora consegue mandar o cabeçalho de autor.
func TestIntegracaoSoNosLembretes(t *testing.T) {
	token := strings.Repeat("t", 40)
	ambiente := map[string]string{"SEGREDO": "s", "CHAVE": strings.Repeat("k", 32), "TOKEN_IPHONE": token}
	l, err := Novo(Config{
		URLBase: "https://rex.exemplo.ts.net", ClientID: clienteDeTeste, ClientSecretAmbiente: "SEGREDO", ChaveAmbiente: "CHAVE",
		Emails: []string{"dono@exemplo.com"}, Integracoes: []Integracao{{Nome: "iphone", TokenAmbiente: "TOKEN_IPHONE"}},
	}, func(k string) string { return ambiente[k] })
	if err != nil {
		t.Fatal(err)
	}
	var autor string
	h := l.Proteger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { autor = r.Header.Get(CabecalhoAutor) }))
	chamar := func(metodo, caminho, tok, autorForjado string) int {
		autor = ""
		r := httptest.NewRequest(metodo, caminho, nil)
		if tok != "" {
			r.Header.Set(CabecalhoIntegracao, tok)
		}
		if autorForjado != "" {
			r.Header.Set(CabecalhoAutor, autorForjado)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for _, c := range []struct{ metodo, caminho string }{
		{"GET", "/lembretes/L-0001"}, {"PATCH", "/lembretes/L-0001"}, {"POST", "/lembretes/L-0001/adiamento"}, {"POST", "/lembretes/L-0001/confirmacao"},
	} {
		if code := chamar(c.metodo, c.caminho, token, ""); code != 200 || autor != "iphone" {
			t.Errorf("%s %s: %d, autor %q", c.metodo, c.caminho, code, autor)
		}
	}
	for _, c := range []struct{ metodo, caminho string }{
		{"POST", "/lembretes"}, {"GET", "/lembretes"}, {"POST", "/lembretes/L-0001/cancelamento"}, {"GET", "/tarefas"}, {"DELETE", "/lembretes/L-0001"},
	} {
		if code := chamar(c.metodo, c.caminho, token, ""); code != http.StatusForbidden {
			t.Errorf("%s %s com token: %d, esperava 403", c.metodo, c.caminho, code)
		}
	}
	if code := chamar("GET", "/lembretes/L-0001", strings.Repeat("x", 40), ""); code != http.StatusUnauthorized {
		t.Errorf("token errado: %d", code)
	}
	if code := chamar("GET", "/lembretes/L-0001", "", "iphone"); code != http.StatusUnauthorized {
		t.Errorf("sem token e com autor forjado: %d", code)
	}
	// O cabeçalho de autor mandado por fora é trocado pelo nome da integração.
	if code := chamar("GET", "/lembretes/L-0001", token, "S-0001"); code != 200 || autor != "iphone" {
		t.Errorf("autor forjado com token: %d %q", code, autor)
	}
	// Token curto demais na configuração é recusado ao carregar.
	ambiente["TOKEN_IPHONE"] = "curto"
	if _, err := Novo(Config{
		URLBase: "https://rex.exemplo.ts.net", ClientID: clienteDeTeste, ClientSecretAmbiente: "SEGREDO", ChaveAmbiente: "CHAVE",
		Emails: []string{"dono@exemplo.com"}, Integracoes: []Integracao{{Nome: "iphone", TokenAmbiente: "TOKEN_IPHONE"}},
	}, func(k string) string { return ambiente[k] }); err == nil {
		t.Error("token curto deveria ser recusado")
	}
}
