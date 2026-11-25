// Package login protege o frontend e a API com login pelo Google, liberado só para os e-mails
// da configuração.
//
// O login vale para os endereços que o servidor marca como "com login" (os expostos ao navegador,
// atrás de HTTPS); os endereços locais, usados pelo MCP, pelo lançador e pelas integrações,
// continuam abertos. O fluxo é o OAuth 2.0 com código e PKCE; o id_token vem direto do token
// endpoint do Google, por TLS, e por isso as suas afirmações valem sem conferir a assinatura
// (OpenID Connect Core, 3.1.3.7). Quem passa ganha um cookie assinado com HMAC.
package login

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"
)

// Caminhos do login; ficam livres de autenticação.
const (
	CaminhoEntrar  = "/login/google"
	CaminhoRetorno = "/login/retorno"
	CaminhoSair    = "/login/sair"
)

const (
	cookieSessao = "rex_login"
	cookieFluxo  = "rex_login_fluxo"
	duracaoFluxo = 10 * time.Minute
	// duracaoPadrao é quanto vale um login sem `duracao_horas` na configuração.
	duracaoPadrao = 30 * 24 * time.Hour
	// tamanhoMinimoChave em bytes: a chave assina os cookies.
	tamanhoMinimoChave = 32
)

// Config é o arquivo JSON do login. Os segredos nunca ficam nele: vêm das variáveis nomeadas.
type Config struct {
	// URLBase é a origem pública do frontend (https://…), usada para montar o redirect URI
	// <url_base>/login/retorno, que tem de estar cadastrado no cliente OAuth do Google.
	URLBase string `json:"url_base"`
	// ClientID do cliente OAuth do Google (tipo "Aplicativo da Web").
	ClientID string `json:"client_id"`
	// ClientSecretAmbiente é o nome da variável com o client secret.
	ClientSecretAmbiente string `json:"client_secret_ambiente"`
	// ChaveAmbiente é o nome da variável com a chave (≥ 32 bytes) que assina os cookies. Trocá-la
	// desfaz todos os logins.
	ChaveAmbiente string `json:"chave_ambiente"`
	// Emails liberados; qualquer outro é recusado mesmo com conta Google válida.
	Emails []string `json:"emails"`
	// DuracaoHoras de um login; padrão 30 dias.
	DuracaoHoras int `json:"duracao_horas"`
	// Integracoes entram sem cookie, por token, só nas rotas dos lembretes (ver integracoes.go).
	Integracoes []Integracao `json:"integracoes,omitempty"`
	// InstrucoesDoConector são acrescentadas às instruções do conector do Claude (/mcp): as regras
	// desta instalação (organizações, a quem mandar o que não tem sessão), que o código não conhece.
	InstrucoesDoConector string `json:"instrucoes_do_conector,omitempty"`
}

// Login é a configuração carregada e validada.
type Login struct {
	oauth   *oauth2.Config
	base    string
	chave   []byte
	emails  []string
	duracao time.Duration
	seguro  bool
	agora   func() time.Time
	// conector, quando ligado, serve o MCP remoto em /mcp (ver conector.go).
	conector *conector
	// integracoes entram por token nas rotas dos lembretes (ver integracoes.go).
	integracoes []integracao
	// trocar troca o código pelo id_token; é substituída nos testes.
	trocar               func(ctx context.Context, codigo, verificador string) (string, error)
	instrucoesDoConector string
}

// Carregar lê e valida o arquivo.
func Carregar(caminho string) (*Login, error) {
	conteudo, err := os.ReadFile(caminho)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(conteudo, &cfg); err != nil {
		return nil, fmt.Errorf("configuração de login: %w", err)
	}
	return Novo(cfg, os.Getenv)
}

// InstrucoesDoConector devolve o complemento da instalação para as instruções do conector.
func (l *Login) InstrucoesDoConector() string { return l.instrucoesDoConector }

// Novo valida a configuração; ambiente lê as variáveis (os.Getenv fora dos testes).
func Novo(cfg Config, ambiente func(string) string) (*Login, error) {
	base := strings.TrimRight(cfg.URLBase, "/")
	switch {
	case !strings.HasPrefix(base, "https://") && !strings.HasPrefix(base, "http://localhost") && !strings.HasPrefix(base, "http://127.0.0.1"):
		return nil, fmt.Errorf("configuração de login: url_base precisa ser https:// (o Google só aceita http em localhost): %q", cfg.URLBase)
	case cfg.ClientID == "":
		return nil, errors.New("configuração de login: falta client_id")
	case cfg.ClientSecretAmbiente == "" || ambiente(cfg.ClientSecretAmbiente) == "":
		return nil, fmt.Errorf("configuração de login: o client secret precisa estar na variável %q", cfg.ClientSecretAmbiente)
	case cfg.ChaveAmbiente == "" || len(ambiente(cfg.ChaveAmbiente)) < tamanhoMinimoChave:
		return nil, fmt.Errorf("configuração de login: a chave dos cookies precisa estar na variável %q, com pelo menos %d caracteres", cfg.ChaveAmbiente, tamanhoMinimoChave)
	case len(cfg.Emails) == 0:
		return nil, errors.New("configuração de login: nenhum e-mail liberado")
	}
	l := &Login{
		oauth: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: ambiente(cfg.ClientSecretAmbiente),
			Endpoint:     endpoints.Google,
			RedirectURL:  base + CaminhoRetorno,
			Scopes:       []string{"openid", "email"},
		},
		base:    base,
		chave:   []byte(ambiente(cfg.ChaveAmbiente)),
		duracao: duracaoPadrao,
		seguro:  strings.HasPrefix(base, "https://"),
		agora:   time.Now,

		instrucoesDoConector: strings.TrimSpace(cfg.InstrucoesDoConector),
	}
	for _, e := range cfg.Emails {
		l.emails = append(l.emails, strings.ToLower(strings.TrimSpace(e)))
	}
	if cfg.DuracaoHoras > 0 {
		l.duracao = time.Duration(cfg.DuracaoHoras) * time.Hour
	}
	integracoes, err := carregarIntegracoes(cfg.Integracoes, ambiente)
	if err != nil {
		return nil, err
	}
	l.integracoes = integracoes
	l.trocar = l.trocarNoGoogle
	return l, nil
}

// Proteger devolve proximo atrás do login: sem cookie válido, a navegação vai para o Google e as
// chamadas da API recebem 401.
func (l *Login) Proteger(proximo http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(CaminhoEntrar, l.entrar)
	mux.HandleFunc(CaminhoRetorno, l.retorno)
	mux.HandleFunc(CaminhoSair, l.sair)
	if l.conector != nil {
		l.rotasDoConector(mux)
	}
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del(CabecalhoAutor)
		if nome, presente := l.integracaoDa(r); presente {
			switch {
			case nome == "":
				responderErro(w, http.StatusUnauthorized, "token de integração inválido")
			case !rotaDaIntegracao(r):
				responderErro(w, http.StatusForbidden, "o token de integração só lê, altera, adia e confirma lembretes")
			default:
				r.Header.Set(CabecalhoAutor, nome)
				proximo.ServeHTTP(w, r)
			}
			return
		}
		if _, ok := l.emailDoCookie(r); ok {
			proximo.ServeHTTP(w, r)
			return
		}
		if navegacao(r) {
			http.Redirect(w, r, CaminhoEntrar, http.StatusFound)
			return
		}
		// O cabeçalho separa este 401 do de token de sessão inválido, que a API também devolve.
		w.Header().Set("X-Rex-Login", "necessario")
		responderErro(w, http.StatusUnauthorized, "login necessário")
	}))
	return mux
}

// navegacao diz se a requisição é o navegador abrindo uma página (e não o fetch da API).
func navegacao(r *http.Request) bool {
	return r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html")
}

func responderErro(w http.ResponseWriter, status int, mensagem string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"mensagem": mensagem})
}

// O fluxo guarda state, verificador do PKCE e o caminho para onde voltar (?voltar=, só deste
// servidor) num cookie curto, assinado: nada fica no servidor.

func (l *Login) entrar(w http.ResponseWriter, r *http.Request) {
	state := aleatorio()
	verificador := oauth2.GenerateVerifier()
	l.gravarCookie(w, cookieFluxo, state+"|"+verificador+"|"+voltarLocal(r.URL.Query().Get("voltar")), CaminhoRetorno, duracaoFluxo)
	url := l.oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verificador), oauth2.SetAuthURLParam("prompt", "select_account"))
	http.Redirect(w, r, url, http.StatusFound)
}

func (l *Login) retorno(w http.ResponseWriter, r *http.Request) {
	fluxo, ok := l.lerCookie(r, cookieFluxo)
	l.apagarCookie(w, cookieFluxo, CaminhoRetorno)
	partes := append(strings.SplitN(fluxo, "|", 3), "", "")
	state, verificador, voltar := partes[0], partes[1], partes[2]
	q := r.URL.Query()
	switch {
	case q.Get("error") != "":
		responderErro(w, http.StatusUnauthorized, "o Google recusou o login: "+q.Get("error"))
		return
	case !ok || state == "" || !hmac.Equal([]byte(state), []byte(q.Get("state"))):
		responderErro(w, http.StatusBadRequest, "login expirado ou de outra aba; tente de novo em "+CaminhoEntrar)
		return
	}
	idToken, err := l.trocar(r.Context(), q.Get("code"), verificador)
	if err != nil {
		log.Printf("login: troca do código: %v", err)
		responderErro(w, http.StatusBadGateway, "não deu para confirmar o login com o Google")
		return
	}
	email, err := l.emailLiberado(idToken)
	if err != nil {
		log.Printf("login: recusado: %v", err)
		responderErro(w, http.StatusForbidden, err.Error())
		return
	}
	log.Printf("login: %s entrou", email)
	l.gravarCookie(w, cookieSessao, email, "/", l.duracao)
	if voltar == "" {
		voltar = "/"
	}
	http.Redirect(w, r, voltar, http.StatusFound)
}

func (l *Login) sair(w http.ResponseWriter, r *http.Request) {
	l.apagarCookie(w, cookieSessao, "/")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "Você saiu do rex. Para entrar de novo: %s\n", CaminhoEntrar)
}

func (l *Login) trocarNoGoogle(ctx context.Context, codigo, verificador string) (string, error) {
	token, err := l.oauth.Exchange(ctx, codigo, oauth2.VerifierOption(verificador))
	if err != nil {
		return "", err
	}
	idToken, _ := token.Extra("id_token").(string)
	if idToken == "" {
		return "", errors.New("a resposta do Google veio sem id_token")
	}
	return idToken, nil
}

// emailLiberado lê as afirmações do id_token, recebido direto do Google por TLS, e confere
// emissor, audiência, validade, e-mail verificado e a lista de e-mails.
func (l *Login) emailLiberado(idToken string) (string, error) {
	partes := strings.Split(idToken, ".")
	if len(partes) != 3 {
		return "", errors.New("id_token malformado")
	}
	bruto, err := base64.RawURLEncoding.DecodeString(partes[1])
	if err != nil {
		return "", fmt.Errorf("id_token malformado: %w", err)
	}
	var c struct {
		Iss           string `json:"iss"`
		Aud           string `json:"aud"`
		Exp           int64  `json:"exp"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := json.Unmarshal(bruto, &c); err != nil {
		return "", fmt.Errorf("id_token malformado: %w", err)
	}
	email := strings.ToLower(c.Email)
	switch {
	case c.Iss != "https://accounts.google.com" && c.Iss != "accounts.google.com":
		return "", fmt.Errorf("id_token de outro emissor: %q", c.Iss)
	case c.Aud != l.oauth.ClientID:
		return "", errors.New("id_token de outro cliente OAuth")
	case l.agora().Unix() > c.Exp:
		return "", errors.New("id_token expirado")
	case !c.EmailVerified:
		return "", fmt.Errorf("o e-mail %s não está verificado no Google", email)
	case !slices.Contains(l.emails, email):
		return "", fmt.Errorf("o e-mail %s não tem acesso a este rex", email)
	}
	return email, nil
}

// Cookies: valor|expiração em unix, assinados com HMAC-SHA256 e em base64.

func (l *Login) gravarCookie(w http.ResponseWriter, nome, valor, caminho string, duracao time.Duration) {
	expira := l.agora().Add(duracao)
	conteudo := valor + "|" + strconv.FormatInt(expira.Unix(), 10)
	http.SetCookie(w, &http.Cookie{
		Name:     nome,
		Value:    base64.RawURLEncoding.EncodeToString([]byte(conteudo)) + "." + l.assinar(nome, conteudo),
		Path:     caminho,
		Expires:  expira,
		HttpOnly: true,
		Secure:   l.seguro,
		SameSite: http.SameSiteLaxMode,
	})
}

func (l *Login) apagarCookie(w http.ResponseWriter, nome, caminho string) {
	http.SetCookie(w, &http.Cookie{Name: nome, Path: caminho, MaxAge: -1, HttpOnly: true, Secure: l.seguro, SameSite: http.SameSiteLaxMode})
}

func (l *Login) lerCookie(r *http.Request, nome string) (string, bool) {
	c, err := r.Cookie(nome)
	if err != nil {
		return "", false
	}
	dados, assinatura, ok := strings.Cut(c.Value, ".")
	if !ok {
		return "", false
	}
	bruto, err := base64.RawURLEncoding.DecodeString(dados)
	if err != nil {
		return "", false
	}
	conteudo := string(bruto)
	if !hmac.Equal([]byte(assinatura), []byte(l.assinar(nome, conteudo))) {
		return "", false
	}
	i := strings.LastIndex(conteudo, "|")
	expira, err := strconv.ParseInt(conteudo[i+1:], 10, 64)
	if i < 0 || err != nil || l.agora().Unix() > expira {
		return "", false
	}
	return conteudo[:i], true
}

// emailDoCookie confere também a lista atual: tirar um e-mail da configuração desfaz o login dele.
func (l *Login) emailDoCookie(r *http.Request) (string, bool) {
	email, ok := l.lerCookie(r, cookieSessao)
	return email, ok && slices.Contains(l.emails, email)
}

// assinar inclui o nome do cookie, para o valor de um não servir no outro.
func (l *Login) assinar(nome, conteudo string) string {
	m := hmac.New(sha256.New, l.chave)
	m.Write([]byte(nome + "\x00" + conteudo))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func aleatorio() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
