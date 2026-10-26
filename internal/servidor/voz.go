package servidor

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rpagliuca/rex/api"
	"github.com/rpagliuca/rex/internal/voz"
)

// Voz é o que o servidor precisa para o rex voz falar com o agente de uma sessão: a tela, o
// teclado e o transcript dele (o lançador, que conhece o tmux).
type Voz interface {
	Tela(x api.Sessao) (string, error)
	Escrever(x api.Sessao, texto string) error
	Teclar(x api.Sessao, tecla string) error
	Conversa(x api.Sessao) string
}

// ComVoz liga o rex voz aos agentes.
func (s *Servidor) ComVoz(v Voz) {
	s.voz = v
}

// sufixoDaVoz vai depois da frase do Rafael, numa linha só (uma quebra de linha mandaria a frase
// antes da hora): diz ao agente de onde ela veio.
const sufixoDaVoz = " — (ditado pelo rex voz: responda curto e em texto corrido; a resposta será resumida em voz)"

// sufixosDaVoz são os sufixos tirados das frases lidas, o atual e o de quando o rex voz era o atalho
// da Siri (transcripts já gravados).
var sufixosDaVoz = []string{sufixoDaVoz, " — (ditado à Siri pelo rex voz: responda curto e em texto corrido; a resposta será resumida em voz)"}

func semSufixo(texto string) string {
	for _, sufixo := range sufixosDaVoz {
		texto = strings.TrimSuffix(texto, sufixo)
	}
	return texto
}

// intervaloDaEspera é de quanto em quanto tempo as esperas olham de novo o agente.
var intervaloDaEspera = time.Second

// sessaoPorId acha a sessão pelo id dela (S-0001) ou pelo da tarefa com sessão ativa (T-0001).
func (s *Servidor) sessaoPorId(id string) (api.Sessao, int64, error) {
	id = strings.ToUpper(strings.TrimSpace(id))
	if strings.HasPrefix(id, "T-") {
		nt, err := numero(id, "T")
		if err != nil {
			return api.Sessao{}, 0, err
		}
		t, err := carregarTarefa(s.db, nt)
		if err != nil {
			return api.Sessao{}, 0, err
		}
		if t.SessaoAtiva == nil {
			return api.Sessao{}, 0, falha(http.StatusConflict, "a tarefa %s não tem sessão ativa", id)
		}
		id = *t.SessaoAtiva
	}
	n, err := numero(id, "S")
	if err != nil {
		return api.Sessao{}, 0, err
	}
	x, err := carregarSessao(s.db, n)
	return x, n, err
}

// estadoDoAgente lê a tela do agente da sessão.
func (s *Servidor) estadoDoAgente(x api.Sessao) (api.EstadoDaVoz, *string) {
	switch {
	case x.Status != api.Ativa:
		return api.VozEncerrada, nil
	case s.voz == nil:
		return api.VozSemAgente, nil
	}
	tela, err := s.voz.Tela(x)
	if err != nil {
		return api.VozSemAgente, nil
	}
	estado, fim := voz.LerTela(tela)
	if fim == "" {
		return api.EstadoDaVoz(estado), nil
	}
	return api.EstadoDaVoz(estado), &fim
}

// lerConversa lê o transcript do agente depois do ponteiro do que o Rafael ouviu.
func (s *Servidor) lerConversa(x api.Sessao, n int64) (voz.Leitura, error) {
	if s.voz == nil {
		return voz.Leitura{}, nil
	}
	caminho := s.voz.Conversa(x)
	if caminho == "" {
		return voz.Leitura{}, nil
	}
	var lido sql.NullString
	if err := s.db.QueryRow(`SELECT voz_lido_ate FROM sessoes WHERE id = ?`, n).Scan(&lido); err != nil {
		return voz.Leitura{}, err
	}
	l, err := voz.Ler(caminho, lido.String)
	if errors.Is(err, os.ErrNotExist) {
		return voz.Leitura{}, nil // agente que ainda não escreveu a conversa
	}
	return l, err
}

// estadoDaVoz é a sessão lida, com o ponteiro novo que não vai na resposta.
type estadoDaVoz struct {
	api.Voz
	ate string
}

func (s *Servidor) lerVoz(x api.Sessao, n int64) (estadoDaVoz, error) {
	v := estadoDaVoz{Voz: api.Voz{Sessao: x.Id, Nome: x.Nome, Tarefas: x.Tarefas, Novidades: []api.NovidadeDeVoz{}}}
	v.Estado, v.Tela = s.estadoDoAgente(x)
	l, err := s.lerConversa(x, n)
	if err != nil {
		return v, err
	}
	v.ate, v.Omitidas = l.Ate, l.Omitidas
	for _, n := range l.Novidades {
		nova := api.NovidadeDeVoz{De: api.DoAgente, Texto: semSufixo(n.Texto), Em: n.Em}
		switch {
		case n.DeRafael:
			nova.De = api.DoRafael
		case n.Origem != "":
			origem := n.Origem
			nova.De, nova.Origem = api.DaSessao, &origem
		}
		if n.Pedido > 0 {
			pedido := n.Pedido
			nova.Pedido = &pedido
		}
		v.Novidades = append(v.Novidades, nova)
	}
	return v, nil
}

// agenteFalouDepois diz se há texto do agente mais novo que o momento dado.
func agenteFalouDepois(v estadoDaVoz, desde time.Time) bool {
	for _, n := range v.Novidades {
		if n.De == api.DoAgente && n.Em.After(desde) {
			return true
		}
	}
	return false
}

func (s *Servidor) ObterVoz(w http.ResponseWriter, r *http.Request, id api.Id, params api.ObterVozParams) {
	esperar := 0
	if params.Esperar != nil {
		esperar = *params.Esperar
	}
	if esperar < 0 || esperar > 20 {
		responderErro(w, falha(http.StatusBadRequest, "esperar vai de 0 a 20 segundos"))
		return
	}
	x, n, err := s.sessaoPorId(id)
	if err != nil {
		responderErro(w, err)
		return
	}
	inicio := s.agora()
	limite := time.Now().Add(time.Duration(esperar) * time.Second)
	v, err := s.lerVoz(x, n)
	for err == nil && v.Estado == api.VozTrabalhando && !agenteFalouDepois(v, inicio) && time.Now().Before(limite) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(intervaloDaEspera):
		}
		v, err = s.lerVoz(x, n)
	}
	if err != nil {
		responderErro(w, err)
		return
	}
	if v.ate != "" && params.Ouvir != nil && *params.Ouvir {
		if _, err := s.db.Exec(`UPDATE sessoes SET voz_lido_ate = ? WHERE id = ?`, v.ate, n); err != nil {
			responderErro(w, err)
			return
		}
	}
	responder(w, http.StatusOK, v.Voz)
}

func (s *Servidor) PanoramaDaVoz(w http.ResponseWriter, r *http.Request) {
	sessoes, err := listarSessoes(s.db, `WHERE s.status = 'ativa' ORDER BY s.id`)
	if err != nil {
		responderErro(w, err)
		return
	}
	titulos := map[string]string{}
	out := []api.PanoramaDeVoz{}
	for _, x := range sessoes {
		n, err := numero(x.Id, "S")
		if err != nil {
			responderErro(w, err)
			return
		}
		p := api.PanoramaDeVoz{Sessao: x.Id, Nome: x.Nome, Tarefas: x.Tarefas, Titulos: &[]string{}, PalavrasChave: &[]string{}}
		p.Estado, _ = s.estadoDoAgente(x)
		if x.Nome != nil {
			*p.PalavrasChave = strings.FieldsFunc(*x.Nome, func(r rune) bool { return r == '-' || r == '_' || r == ' ' })
		}
		if x.Aguardando != nil {
			p.Aguardando = &x.Aguardando.Mensagem
		}
		for _, t := range x.Tarefas {
			if _, ok := titulos[t]; !ok {
				if nt, err := numero(t, "T"); err == nil {
					if tarefa, err := carregarTarefa(s.db, nt); err == nil {
						titulos[t] = tarefa.Titulo
					}
				}
			}
			*p.Titulos = append(*p.Titulos, titulos[t])
		}
		l, err := s.lerConversa(x, n)
		if err != nil {
			responderErro(w, err)
			return
		}
		p.NaoOuvidas = l.Respostas
		if l.Ultima != nil {
			texto := comecoDoTexto(semSufixo(l.Ultima.Texto))
			p.Ultima, p.UltimaEm = &texto, &l.Ultima.Em
		}
		out = append(out, p)
	}
	responder(w, http.StatusOK, out)
}

// TamanhoNoPanorama é até onde vai o último texto do agente no panorama: a varredura de todas as
// sessões precisa ser leve, e o texto inteiro vem na situação da sessão.
const TamanhoNoPanorama = 200

// comecoDoTexto corta o texto no fim da frase ou da palavra antes do limite do panorama.
func comecoDoTexto(texto string) string {
	r := []rune(strings.Join(strings.Fields(texto), " "))
	if len(r) <= TamanhoNoPanorama {
		return string(r)
	}
	corte := string(r[:TamanhoNoPanorama])
	if i := strings.LastIndexAny(corte, ".!?:"); i >= TamanhoNoPanorama/2 {
		return corte[:i+1]
	}
	if i := strings.LastIndexByte(corte, ' '); i > 0 {
		corte = corte[:i]
	}
	return corte + "…"
}

// sessaoComAgente carrega a sessão, que precisa estar ativa e com agente.
func (s *Servidor) sessaoComAgente(id string) (api.Sessao, int64, error) {
	x, n, err := s.sessaoPorId(id)
	if err != nil {
		return x, 0, err
	}
	if x.Status != api.Ativa {
		return x, 0, falha(http.StatusConflict, "a sessão %s já foi encerrada", x.Id)
	}
	if s.voz == nil {
		return x, 0, falha(http.StatusConflict, "este rex não fala com agentes (sem lançador)")
	}
	return x, n, nil
}

func (s *Servidor) telaDaVoz(x api.Sessao) (voz.Estado, error) {
	tela, err := s.voz.Tela(x)
	if err != nil {
		return "", falha(http.StatusConflict, "o tmux do agente da sessão %s não foi achado", x.Id)
	}
	estado, _ := voz.LerTela(tela)
	return estado, nil
}

func (s *Servidor) EnviarVoz(w http.ResponseWriter, r *http.Request, id api.Id) {
	var corpo api.MensagemDeVoz
	if err := ler(r, &corpo); err != nil {
		responderErro(w, err)
		return
	}
	texto := strings.Join(strings.Fields(corpo.Texto), " ")
	if texto == "" {
		responderErro(w, falha(http.StatusBadRequest, "o texto é obrigatório"))
		return
	}
	x, n, err := s.sessaoComAgente(id)
	if err != nil {
		responderErro(w, err)
		return
	}
	estado, err := s.telaDaVoz(x)
	if err != nil {
		responderErro(w, err)
		return
	}
	if estado == voz.PedindoPermissao {
		responderErro(w, falha(http.StatusConflict, "o agente está esperando uma escolha na tela: responda antes com a tecla"))
		return
	}
	var pedido int
	if err := s.db.QueryRow(`SELECT voz_pedidos + 1 FROM sessoes WHERE id = ?`, n).Scan(&pedido); err != nil {
		responderErro(w, err)
		return
	}
	if err := s.voz.Escrever(x, voz.Prefixo+strconv.Itoa(pedido)+"] "+texto+sufixoDaVoz); err != nil {
		responderErro(w, err)
		return
	}
	err = s.emTransacao(func(tx *sql.Tx) error {
		quem, err := autor(tx, r)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE sessoes SET voz_pedidos = ? WHERE id = ?`, pedido, n); err != nil {
			return err
		}
		return s.registrarEvento(tx, quem, x.Id, "voz_pedido", map[string]any{"pedido": pedido, "texto": texto})
	})
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusCreated, api.PedidoDeVoz{Pedido: pedido, Sessao: x.Id, Estado: api.EstadoDaVoz(estado)})
}

// EnviarASessao escreve no agente de uma sessão a mensagem da sessão dona do token, identificada e
// com a instrução de responder pela mesma via (R-0538).
func (s *Servidor) EnviarASessao(w http.ResponseWriter, r *http.Request, id api.Id) {
	var corpo api.MensagemDeVoz
	if err := ler(r, &corpo); err != nil {
		responderErro(w, err)
		return
	}
	texto := strings.Join(strings.Fields(corpo.Texto), " ")
	if texto == "" {
		responderErro(w, falha(http.StatusBadRequest, "o texto é obrigatório"))
		return
	}
	if tokenDaRequisicao(r) == "" {
		responderErro(w, falha(http.StatusUnauthorized, "esta operação exige o token da sessão que manda a mensagem"))
		return
	}
	var origem api.Sessao
	err := s.emTransacao(func(tx *sql.Tx) error {
		quem, err := autor(tx, r)
		if err != nil {
			return err
		}
		n, err := numero(quem, "S")
		if err != nil {
			return err
		}
		origem, err = carregarSessao(tx, n)
		return err
	})
	if err != nil {
		responderErro(w, err)
		return
	}
	x, _, err := s.sessaoComAgente(id)
	if err != nil {
		responderErro(w, err)
		return
	}
	if x.Id == origem.Id {
		responderErro(w, falha(http.StatusBadRequest, "a sessão %s é a deste chat: mande a mensagem para outra", x.Id))
		return
	}
	estado, err := s.telaDaVoz(x)
	if err != nil {
		responderErro(w, err)
		return
	}
	if estado == voz.PedindoPermissao {
		responderErro(w, falha(http.StatusConflict, "o agente da sessão %s está esperando uma escolha do Rafael na tela: tente de novo depois", x.Id))
		return
	}
	nome := ""
	if origem.Nome != nil {
		nome = *origem.Nome
	}
	if err := s.voz.Escrever(x, voz.MensagemDeSessao(origem.Id, nome, texto)); err != nil {
		responderErro(w, err)
		return
	}
	err = s.emTransacao(func(tx *sql.Tx) error {
		return s.registrarEvento(tx, origem.Id, x.Id, "mensagem_de_sessao", map[string]any{"de": origem.Id, "texto": texto})
	})
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusCreated, api.MensagemEntreSessoes{De: origem.Id, Para: x.Id, Estado: api.EstadoDaVoz(estado)})
}

// esperaDaTecla dá tempo à tela de mudar antes de lê-la de novo.
var esperaDaTecla = 800 * time.Millisecond

func (s *Servidor) TeclarVoz(w http.ResponseWriter, r *http.Request, id api.Id) {
	var corpo api.TeclaDeVoz
	if err := ler(r, &corpo); err != nil {
		responderErro(w, err)
		return
	}
	switch corpo.Tecla {
	case api.Tecla1, api.Tecla2, api.Tecla3, api.Tecla4, api.TeclaEnter, api.TeclaEscape:
	default:
		responderErro(w, falha(http.StatusBadRequest, "tecla desconhecida: %q", corpo.Tecla))
		return
	}
	x, n, err := s.sessaoComAgente(id)
	if err != nil {
		responderErro(w, err)
		return
	}
	if corpo.Tecla != api.TeclaEscape {
		estado, err := s.telaDaVoz(x)
		if err != nil {
			responderErro(w, err)
			return
		}
		if estado != voz.PedindoPermissao {
			responderErro(w, falha(http.StatusConflict, "o agente não está esperando escolha: a tecla %s cairia na conversa", corpo.Tecla))
			return
		}
	}
	if err := s.voz.Teclar(x, string(corpo.Tecla)); err != nil {
		responderErro(w, err)
		return
	}
	err = s.emTransacao(func(tx *sql.Tx) error {
		quem, err := autor(tx, r)
		if err != nil {
			return err
		}
		return s.registrarEvento(tx, quem, x.Id, "voz_tecla", map[string]any{"tecla": string(corpo.Tecla)})
	})
	if err != nil {
		responderErro(w, err)
		return
	}
	time.Sleep(esperaDaTecla)
	v, err := s.lerVoz(x, n)
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusOK, v.Voz)
}
