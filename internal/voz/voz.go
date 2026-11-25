// Package voz lê, para o rex voz, a conversa de um agente do Claude Code e
// a tela do tmux dele.
//
// A conversa é o transcript em ~/.claude/projects/<diretório>/<chat>.jsonl: uma linha por evento,
// cada uma com um uuid. O ponteiro do que o usuário já ouviu é o uuid da última linha entregue; as
// novidades são as falas do usuário e os textos do agente depois dele.
package voz

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Prefixo marca no agente as frases que vieram do rex voz: "[rex voz · pedido 3] …".
const Prefixo = "[rex voz · pedido "

// PrefixoDeSessao marca no agente as mensagens de outra sessão: "[rex · de S-0001 nome] …".
const PrefixoDeSessao = "[rex · de "

// MensagemDeSessao monta, numa linha só (uma quebra de linha mandaria a frase antes da hora), o que a
// sessão de origem escreve no agente de outra: quem mandou e como responder, para a resposta voltar
// pelo rex ao chat da origem em vez de ficar só na conversa do destino.
func MensagemDeSessao(origem, nome, texto string) string {
	quem := origem
	if nome != "" {
		quem += " " + nome
	}
	return PrefixoDeSessao + quem + "] " + texto + " — (mensagem da sessão " + quem + ", não do usuário: " +
		"responda com a ferramenta enviar_a_sessao do rex para " + origem + "; a resposta entra no chat dela)"
}

var instrucaoDeSessao = regexp.MustCompile(` — \(mensagem da sessão S-\d+[^)]*\)$`)

// Novidade é uma fala do usuário, uma mensagem de outra sessão ou um texto do agente.
type Novidade struct {
	DoUsuario bool
	Origem    string // a sessão (S-0001) que mandou a mensagem; vazio quando não veio de outra sessão
	Texto     string
	Pedido    int // 0 quando não veio do rex voz nem responde a um pedido dele
	Em        time.Time
}

// Leitura é o que aconteceu depois do ponteiro.
type Leitura struct {
	Novidades []Novidade
	Omitidas  int    // novidades mais antigas que o limite deixou de fora
	Ate       string // uuid da última linha lida: o ponteiro novo, se o usuário ouvir tudo
	// Ultima é o último texto do agente no transcript inteiro, antes ou depois do ponteiro.
	Ultima *Novidade
	// Respostas conta os textos do agente depois do ponteiro e da última fala do usuário: o que ele
	// ainda não viu, nem pela voz nem pelo chat.
	Respostas int
}

// Limites da leitura: o rex voz resume para a voz, não precisa de tudo.
const (
	MaximoDeNovidades = 6
	TamanhoDoTexto    = 1500
	// semPonteiro é quantas novidades entram quando a sessão nunca foi acompanhada.
	semPonteiro = 3
)

// Arquivo acha o transcript do chat sob a raiz dos projetos do Claude Code, ou "".
func Arquivo(projetos, chat string) string {
	if chat == "" || strings.ContainsAny(chat, "/\\*?[") {
		return ""
	}
	achados, _ := filepath.Glob(filepath.Join(projetos, "*", chat+".jsonl"))
	if len(achados) == 0 {
		return ""
	}
	return achados[0]
}

type linha struct {
	Type        string    `json:"type"`
	UUID        string    `json:"uuid"`
	IsSidechain bool      `json:"isSidechain"`
	IsMeta      bool      `json:"isMeta"`
	Timestamp   time.Time `json:"timestamp"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	// Attachment traz a frase mandada com o agente no meio de um turno: queued_command.
	Attachment struct {
		Type   string `json:"type"`
		Prompt string `json:"prompt"`
	} `json:"attachment"`
}

var (
	pedidoNoTexto = regexp.MustCompile(`^\[rex voz · pedido (\d+)\]\s*`)
	sessaoNoTexto = regexp.MustCompile(`^\[rex · de (S-\d+)[^\]]*\]\s*`)
)

// Ler devolve as novidades do transcript depois da linha `depois` (uuid). Ponteiro vazio ou que
// não está mais no arquivo: só as últimas, como se fosse a primeira vez.
func Ler(caminho, depois string) (Leitura, error) {
	f, err := os.Open(caminho)
	if err != nil {
		return Leitura{}, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<20), 64<<20)
	var todas []Novidade
	var ate string
	var ultima *Novidade
	respostas := 0
	achou := false
	pedido := 0
	for s.Scan() {
		var l linha
		if json.Unmarshal(s.Bytes(), &l) != nil || l.UUID == "" || l.IsSidechain {
			continue
		}
		ate = l.UUID
		if l.UUID == depois {
			achou, todas, respostas = true, nil, 0
			continue
		}
		switch l.Type {
		case "user", "attachment":
			var texto string
			if l.Type == "attachment" {
				if l.Attachment.Type != "queued_command" {
					continue
				}
				texto = l.Attachment.Prompt
			} else if l.IsMeta || json.Unmarshal(l.Message.Content, &texto) != nil {
				continue // resultado de ferramenta
			}
			texto = strings.TrimSpace(texto)
			if texto == "" || strings.HasPrefix(texto, "<") {
				continue // comando local, lembrete do sistema
			}
			if m := sessaoNoTexto.FindStringSubmatch(texto); m != nil {
				// Outra sessão falou: não é o usuário, então o que ele ainda não viu continua contando.
				pedido = 0
				texto = instrucaoDeSessao.ReplaceAllString(texto[len(m[0]):], "")
				todas = append(todas, Novidade{Origem: m[1], Texto: encurtar(texto), Em: l.Timestamp})
				continue
			}
			pedido, respostas = 0, 0
			if m := pedidoNoTexto.FindStringSubmatch(texto); m != nil {
				pedido, _ = strconv.Atoi(m[1])
				texto = texto[len(m[0]):]
			}
			todas = append(todas, Novidade{DoUsuario: true, Texto: encurtar(texto), Pedido: pedido, Em: l.Timestamp})
		case "assistant":
			var blocos []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
			}
			if json.Unmarshal(l.Message.Content, &blocos) != nil {
				continue
			}
			for _, b := range blocos {
				texto := b.Text
				if b.Type == "thinking" {
					// O raciocínio fica gravado vazio; um thinking com texto é a narração que o agente
					// mostra no meio do turno, como a resposta a um pedido antes de seguir com ferramentas.
					texto = b.Thinking
				} else if b.Type != "text" {
					continue
				}
				if texto = strings.TrimSpace(texto); texto != "" {
					n := Novidade{Texto: encurtar(texto), Pedido: pedido, Em: l.Timestamp}
					todas, ultima = append(todas, n), &n
					respostas++
				}
			}
		}
	}
	if err := s.Err(); err != nil {
		return Leitura{}, err
	}
	limite := semPonteiro
	if achou {
		limite = MaximoDeNovidades
	}
	r := Leitura{Novidades: todas, Ate: ate, Ultima: ultima, Respostas: respostas}
	if len(todas) > limite {
		r.Omitidas = len(todas) - limite
		r.Novidades = todas[r.Omitidas:]
	}
	if r.Novidades == nil {
		r.Novidades = []Novidade{}
	}
	return r, nil
}

// encurtar corta textos longos no começo e no fim: a conclusão do agente costuma estar no fim.
func encurtar(texto string) string {
	r := []rune(texto)
	if len(r) <= TamanhoDoTexto {
		return texto
	}
	metade := TamanhoDoTexto / 2
	return string(r[:metade]) + " […] " + string(r[len(r)-metade:])
}

// Estado do agente, lido da tela do tmux.
type Estado string

const (
	Trabalhando      Estado = "trabalhando"
	Ocioso           Estado = "ocioso"
	PedindoPermissao Estado = "pedindo_permissao"
)

// LinhasDaTela é quanto do fim da tela vai junto quando o agente pede permissão.
const LinhasDaTela = 25

// LerTela classifica a tela do Claude Code: uma pergunta com opções (permissão, escolha) espera o
// usuário; "esc to interrupt" na barra de baixo é um turno em andamento; o resto é o agente parado.
// Devolve o fim da tela só quando ela pede uma escolha.
func LerTela(tela string) (Estado, string) {
	var linhas []string
	for _, l := range strings.Split(tela, "\n") {
		if strings.TrimSpace(l) != "" {
			linhas = append(linhas, strings.TrimRight(l, " "))
		}
	}
	baixo := strings.ToLower(strings.Join(ultimas(linhas, 12), "\n"))
	switch {
	case strings.Contains(baixo, "esc to cancel") || strings.Contains(baixo, "do you want to") ||
		strings.Contains(baixo, "❯ 1."):
		return PedindoPermissao, strings.Join(ultimas(linhas, LinhasDaTela), "\n")
	case strings.Contains(baixo, "esc to interrupt"):
		return Trabalhando, ""
	}
	return Ocioso, ""
}

func ultimas(l []string, n int) []string {
	if len(l) > n {
		return l[len(l)-n:]
	}
	return l
}
