// Package integracao liga o rex ao que cada instalação quiser, por configuração: ganchos
// (comando ou webhook) chamados a cada evento do log, e o agendador que dispara os lembretes.
//
// Os ganchos leem o log de eventos por um cursor gravado no banco: a entrega segue a ordem do log,
// continua de onde parou depois de reiniciar e não atrasa a API. Cada evento é tentado algumas
// vezes; depois disso vira o evento integracao_falhou e a entrega segue. A fonte de verdade para
// quem precisa de garantia é GET /integracao/pendencias (ver docs/integracao.md).
package integracao

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/rpagliuca/rex/api"
	"github.com/rpagliuca/rex/internal/servidor"
)

// Config vem de um arquivo JSON, particular de cada instalação.
type Config struct {
	// Fuso IANA (ex.: America/Sao_Paulo) dos lembretes. Padrão: o da máquina.
	Fuso string `json:"fuso"`
	// BasePublica do frontend, para o link dos lembretes.
	BasePublica string   `json:"base_publica"`
	Ganchos     []Gancho `json:"ganchos"`
	// Turnos dos lembretes de turno ou dia, em "HH:MM". Padrão: 08:00, 12:00, 13:00 e 18:00.
	Turnos *Turnos `json:"turnos"`
}

// Turnos: onde começam a manhã (e o dia), o almoço, a tarde e a noite.
type Turnos struct {
	Manha  string `json:"manha"`
	Almoco string `json:"almoco"`
	Tarde  string `json:"tarde"`
	Noite  string `json:"noite"`
}

// Gancho é chamado a cada evento cujo tipo casa com Eventos; ou Comando, ou Webhook.
type Gancho struct {
	Nome string `json:"nome"`
	// Eventos: tipos exatos, ou com "*" no fim (lembrete_*).
	Eventos []string `json:"eventos"`
	// Comando: argv, sem shell; aceita {tipo}, {entidade} e {evento}; a notificação vai no stdin.
	Comando []string `json:"comando"`
	Webhook *Webhook `json:"webhook"`
}

type Webhook struct {
	URL string `json:"url"`
	// Cabecalhos: valores "env:NOME" vêm da variável de ambiente NOME.
	Cabecalhos map[string]string `json:"cabecalhos"`
}

// eventoDeFalha nunca é entregue a um gancho: uma falha de entrega não pode gerar outra.
const eventoDeFalha = "integracao_falhou"

// Integracao é a configuração carregada e validada.
type Integracao struct {
	cfg    Config
	fuso   *time.Location
	turnos servidor.Turnos
	// tentativas são as esperas entre uma tentativa e a seguinte.
	tentativas []time.Duration
	// intervalo entre leituras do log.
	intervalo time.Duration
	cliente   *http.Client
	ambiente  func(string) string
}

// Carregar lê e valida o arquivo.
func Carregar(caminho string) (*Integracao, error) {
	conteudo, err := os.ReadFile(caminho)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(conteudo, &cfg); err != nil {
		return nil, fmt.Errorf("configuração de integração: %w", err)
	}
	return Nova(cfg, os.Getenv)
}

// Nova valida a configuração; ambiente lê as variáveis (os.Getenv fora dos testes).
func Nova(cfg Config, ambiente func(string) string) (*Integracao, error) {
	i := &Integracao{
		cfg:        cfg,
		fuso:       time.Local,
		tentativas: []time.Duration{time.Second, 10 * time.Second, time.Minute},
		intervalo:  time.Second,
		cliente:    &http.Client{Timeout: 15 * time.Second},
		ambiente:   ambiente,
	}
	if cfg.Fuso != "" {
		fuso, err := time.LoadLocation(cfg.Fuso)
		if err != nil {
			return nil, fmt.Errorf("configuração de integração: fuso %q: %w", cfg.Fuso, err)
		}
		i.fuso = fuso
	}
	if t := cfg.Turnos; t != nil {
		var err error
		if i.turnos, err = lerTurnos(*t); err != nil {
			return nil, fmt.Errorf("configuração de integração: turnos: %w", err)
		}
	}
	nomes := map[string]bool{}
	for _, g := range cfg.Ganchos {
		switch {
		case g.Nome == "" || nomes[g.Nome]:
			return nil, fmt.Errorf("configuração de integração: todo gancho precisa de um nome único (%q)", g.Nome)
		case len(g.Eventos) == 0:
			return nil, fmt.Errorf("configuração de integração: o gancho %q não assina nenhum evento", g.Nome)
		case (len(g.Comando) == 0) == (g.Webhook == nil):
			return nil, fmt.Errorf("configuração de integração: o gancho %q precisa de comando ou de webhook (um dos dois)", g.Nome)
		case g.Webhook != nil && g.Webhook.URL == "":
			return nil, fmt.Errorf("configuração de integração: o webhook do gancho %q precisa de url", g.Nome)
		}
		nomes[g.Nome] = true
	}
	return i, nil
}

// Opcoes são os parâmetros que a API usa.
func (i *Integracao) Opcoes() servidor.Integracao {
	return servidor.Integracao{Fuso: i.fuso, BasePublica: i.cfg.BasePublica, Turnos: i.turnos}
}

// Conectar liga as opções ao servidor e começa a entregar os eventos aos ganchos, até ctx acabar.
func (i *Integracao) Conectar(ctx context.Context, s *servidor.Servidor) {
	s.ComIntegracao(i.Opcoes())
	for _, g := range i.cfg.Ganchos {
		go i.entregar(ctx, s, g)
	}
}

// lerTurnos lê as horas "HH:MM"; o turno omitido fica com o padrão.
func lerTurnos(t Turnos) (servidor.Turnos, error) {
	out := servidor.TurnosPadrao
	for _, c := range []struct {
		nome, valor string
		destino     *int
	}{{"manha", t.Manha, &out.Manha}, {"almoco", t.Almoco, &out.Almoco}, {"tarde", t.Tarde, &out.Tarde}, {"noite", t.Noite, &out.Noite}} {
		if c.valor == "" {
			continue
		}
		h, err := time.Parse("15:04", c.valor)
		if err != nil {
			return out, fmt.Errorf("%s: use HH:MM (%q)", c.nome, c.valor)
		}
		*c.destino = h.Hour()*60 + h.Minute()
	}
	return out, out.Validar()
}

func casa(g Gancho, tipo string) bool {
	if tipo == eventoDeFalha {
		return false
	}
	for _, padrao := range g.Eventos {
		if prefixo, ok := strings.CutSuffix(padrao, "*"); ok && strings.HasPrefix(tipo, prefixo) || padrao == tipo {
			return true
		}
	}
	return false
}

// entregar segue o log para um gancho. Um gancho novo começa no fim do log: não recebe o histórico.
func (i *Integracao) entregar(ctx context.Context, s *servidor.Servidor, g Gancho) {
	cursor, ok, err := s.Cursor(g.Nome)
	if err == nil && !ok {
		if cursor, err = s.UltimoEvento(); err == nil {
			err = s.GravarCursor(g.Nome, cursor)
		}
	}
	if err != nil {
		log.Printf("gancho %s: cursor: %v", g.Nome, err)
		return
	}
	for {
		eventos, err := s.EventosDepois(cursor, 100)
		if err != nil {
			log.Printf("gancho %s: ler eventos: %v", g.Nome, err)
		}
		for _, e := range eventos {
			if casa(g, e.Tipo) {
				i.entregarEvento(ctx, s, g, e)
			}
			if ctx.Err() != nil {
				return
			}
			cursor = e.Id
			if err := s.GravarCursor(g.Nome, cursor); err != nil {
				log.Printf("gancho %s: gravar cursor: %v", g.Nome, err)
			}
		}
		if len(eventos) == 100 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(i.intervalo):
		}
	}
}

func (i *Integracao) entregarEvento(ctx context.Context, s *servidor.Servidor, g Gancho, e api.Evento) {
	var ultimo error
	for tentativa := 0; tentativa <= len(i.tentativas); tentativa++ {
		if tentativa > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(i.tentativas[tentativa-1]):
			}
		}
		// A notificação é montada a cada tentativa: leva o estado mais recente da entidade.
		n, err := s.Notificacao(e)
		if err == nil {
			err = i.chamar(ctx, g, n)
		}
		if err == nil {
			return
		}
		ultimo = err
		log.Printf("gancho %s: evento %d (%s %s), tentativa %d: %v", g.Nome, e.Id, e.Tipo, e.Entidade, tentativa+1, err)
	}
	if err := s.RegistrarFalhaDeIntegracao(g.Nome, e.Id, ultimo.Error()); err != nil {
		log.Printf("gancho %s: registrar falha: %v", g.Nome, err)
	}
}

func (i *Integracao) chamar(ctx context.Context, g Gancho, n api.Notificacao) error {
	corpo, err := json.Marshal(n)
	if err != nil {
		return err
	}
	ctx, cancelar := context.WithTimeout(ctx, 30*time.Second)
	defer cancelar()
	if g.Webhook != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.Webhook.URL, bytes.NewReader(corpo))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		for nome, valor := range g.Webhook.Cabecalhos {
			if variavel, ok := strings.CutPrefix(valor, "env:"); ok {
				valor = i.ambiente(variavel)
			}
			req.Header.Set(nome, valor)
		}
		resp, err := i.cliente.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return fmt.Errorf("webhook respondeu %d", resp.StatusCode)
		}
		return nil
	}
	troca := strings.NewReplacer("{tipo}", n.Evento.Tipo, "{entidade}", n.Evento.Entidade, "{evento}", strconv.FormatInt(n.Evento.Id, 10))
	args := make([]string, len(g.Comando))
	for j, a := range g.Comando {
		args[j] = troca.Replace(a)
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = bytes.NewReader(corpo)
	saida, err := cmd.CombinedOutput()
	if err != nil {
		var sair *exec.ExitError
		if errors.As(err, &sair) {
			return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(saida)))
		}
		return err
	}
	return nil
}

// Agendar dispara os lembretes vencidos agora (os que venceram com o servidor parado saem como
// atrasados) e depois a cada intervalo, até ctx acabar.
func Agendar(ctx context.Context, s *servidor.Servidor, intervalo time.Duration) {
	for {
		if n, err := s.DispararVencidos(); err != nil {
			log.Printf("agendador: %v", err)
		} else if n > 0 {
			log.Printf("agendador: %d lembrete(s) disparado(s)", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(intervalo):
		}
	}
}
