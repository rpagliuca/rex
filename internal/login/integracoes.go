package login

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"regexp"
)

// Integracao é um serviço de fora (o Lambda dos avisos do iPhone, por exemplo) que chama o
// endereço com login sem cookie, por um token próprio, e só para as rotas dos lembretes: ler,
// alterar (o grau de aviso), adiar e confirmar um lembrete. Criar, cancelar e todo o resto continuam
// exigindo o login pelo Google.
type Integracao struct {
	// Nome aparece como autor dos eventos ("iphone").
	Nome string `json:"nome"`
	// TokenAmbiente é o nome da variável com o token (≥ 32 caracteres), mandado no cabeçalho
	// X-Rex-Integracao.
	TokenAmbiente string `json:"token_ambiente"`
}

// CabecalhoIntegracao leva o token da integração; não é o Authorization, que a API lê como token de
// sessão.
const CabecalhoIntegracao = "X-Rex-Integracao"

// CabecalhoAutor diz à API quem é a integração; o login o apaga de toda requisição que chega e só o
// põe nas que passaram pelo token. Nos endereços locais, abertos, quem chama já é de confiança.
const CabecalhoAutor = "X-Rex-Autor"

var reNomeIntegracao = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)

type integracao struct {
	nome  string
	token []byte
}

// rotasDaIntegracao: as únicas que um token de integração abre.
var rotasDaIntegracao = []struct {
	metodo string
	rota   *regexp.Regexp
}{
	{http.MethodGet, regexp.MustCompile(`^/lembretes/L-[0-9]+$`)},
	{http.MethodPatch, regexp.MustCompile(`^/lembretes/L-[0-9]+$`)},
	{http.MethodPost, regexp.MustCompile(`^/lembretes/L-[0-9]+/(adiamento|confirmacao)$`)},
}

func carregarIntegracoes(cfg []Integracao, ambiente func(string) string) ([]integracao, error) {
	var out []integracao
	for _, i := range cfg {
		if !reNomeIntegracao.MatchString(i.Nome) {
			return nil, fmt.Errorf("configuração de login: nome de integração inválido: %q", i.Nome)
		}
		token := ambiente(i.TokenAmbiente)
		if i.TokenAmbiente == "" || len(token) < tamanhoMinimoChave {
			return nil, fmt.Errorf("configuração de login: o token da integração %s precisa estar na variável %q, com pelo menos %d caracteres",
				i.Nome, i.TokenAmbiente, tamanhoMinimoChave)
		}
		out = append(out, integracao{nome: i.Nome, token: []byte(token)})
	}
	return out, nil
}

// integracaoDa devolve o nome da integração dona do token, se houver; ok falso sem cabeçalho.
func (l *Login) integracaoDa(r *http.Request) (nome string, presente bool) {
	token := r.Header.Get(CabecalhoIntegracao)
	if token == "" {
		return "", false
	}
	for _, i := range l.integracoes {
		if subtle.ConstantTimeCompare([]byte(token), i.token) == 1 {
			return i.nome, true
		}
	}
	return "", true
}

func rotaDaIntegracao(r *http.Request) bool {
	for _, x := range rotasDaIntegracao {
		if r.Method == x.metodo && x.rota.MatchString(r.URL.Path) {
			return true
		}
	}
	return false
}
