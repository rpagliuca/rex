package servidor

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rpagliuca/rex/api"
)

// Integracao são os parâmetros de cada instalação que a API usa (ver docs/integracao.md).
type Integracao struct {
	// Fuso em que os lembretes são interpretados e mostrados. Padrão: o da máquina.
	Fuso *time.Location
	// BasePublica do frontend, para o link dos lembretes; vazia, sem link.
	BasePublica string
	// Turnos dos lembretes de turno ou dia; vazio, TurnosPadrao.
	Turnos Turnos
}

// ComIntegracao liga os parâmetros da instalação.
func (s *Servidor) ComIntegracao(i Integracao) {
	s.integracao = i
}

func (s *Servidor) fuso() *time.Location {
	if s.integracao.Fuso != nil {
		return s.integracao.Fuso
	}
	return time.Local
}

const horizontePadrao = 120

func (s *Servidor) ListarPendencias(w http.ResponseWriter, r *http.Request, params api.ListarPendenciasParams) {
	horizonte := horizontePadrao
	if params.Horizonte != nil {
		horizonte = *params.Horizonte
	}
	if horizonte < 0 || horizonte > 10080 {
		responderErro(w, falha(http.StatusBadRequest, "horizonte entre 0 e 10080 minutos"))
		return
	}
	agora := s.agora()
	ate := agora.Add(time.Duration(horizonte) * time.Minute)
	p := api.Pendencias{GeradoEm: agora, HorizonteAte: ate}
	// Uma leitura só, para o retrato e o cursor serem coerentes.
	err := s.emTransacao(func(tx *sql.Tx) (err error) {
		if err := tx.QueryRow(`SELECT coalesce(max(id), 0) FROM eventos`).Scan(&p.UltimoEvento); err != nil {
			return err
		}
		disparados, err := s.listarLembretes(tx, `status = ?`, string(api.Disparado))
		if err != nil {
			return err
		}
		filtro, args := emSQL([]api.StatusLembrete{api.Agendado, api.Adiado})
		proximos, err := s.listarLembretes(tx, filtro+` AND quando_utc <= ?`, append(args, utc(ate))...)
		if err != nil {
			return err
		}
		p.Lembretes = append(disparados, proximos...)
		linhas, err := tx.Query(`SELECT `+colunasTarefa+` FROM tarefas t WHERE t.status = ? ORDER BY t.atualizada_em`, string(api.EmValidacao))
		if err != nil {
			return err
		}
		p.TarefasEmValidacao = []api.Tarefa{}
		for linhas.Next() {
			t, err := lerTarefa(linhas)
			if err != nil {
				linhas.Close()
				return err
			}
			p.TarefasEmValidacao = append(p.TarefasEmValidacao, t)
		}
		if err := linhas.Close(); err != nil {
			return err
		}
		p.SessoesAguardando, err = listarSessoes(tx, `WHERE s.status = 'ativa' AND s.aguardando IS NOT NULL ORDER BY s.aguardando_desde`)
		return err
	})
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusOK, p)
}

func (s *Servidor) AguardarResposta(w http.ResponseWriter, r *http.Request, id api.Id) {
	var espera api.Espera
	s.operarSessao(w, r, id, &espera, func(tx *sql.Tx, n int64) error {
		if vazio(espera.Mensagem) {
			return falha(http.StatusBadRequest, "a mensagem é obrigatória: é o que o Rafael precisa responder")
		}
		mensagem := strings.TrimSpace(espera.Mensagem)
		if _, err := tx.Exec(`UPDATE sessoes SET aguardando = ?, aguardando_desde = ? WHERE id = ?`, mensagem, s.momento(), n); err != nil {
			return err
		}
		return s.registrarEvento(tx, idSessao(n), idSessao(n), "sessao_aguardando", map[string]any{"mensagem": mensagem})
	})
}

func (s *Servidor) RetomarSessao(w http.ResponseWriter, r *http.Request, id api.Id) {
	s.operarSessao(w, r, id, nil, func(tx *sql.Tx, n int64) error {
		res, err := tx.Exec(`UPDATE sessoes SET aguardando = NULL, aguardando_desde = NULL WHERE id = ? AND aguardando IS NOT NULL`, n)
		if err != nil {
			return err
		}
		if mudou, _ := res.RowsAffected(); mudou == 0 {
			return nil
		}
		return s.registrarEvento(tx, idSessao(n), idSessao(n), "sessao_retomada", nil)
	})
}

// EventosDepois lê até limite eventos com id maior que o cursor, em ordem, para os ganchos.
func (s *Servidor) EventosDepois(cursor int64, limite int) ([]api.Evento, error) {
	linhas, err := s.db.Query(`SELECT id, momento, autor, entidade, tipo, dados FROM eventos WHERE id > ? ORDER BY id LIMIT ?`, cursor, limite)
	if err != nil {
		return nil, err
	}
	defer linhas.Close()
	var eventos []api.Evento
	for linhas.Next() {
		var e api.Evento
		var momento, dados string
		if err := linhas.Scan(&e.Id, &momento, &e.Autor, &e.Entidade, &e.Tipo, &dados); err != nil {
			return nil, err
		}
		e.Momento = lerMomento(momento)
		if err := json.Unmarshal([]byte(dados), &e.Dados); err != nil {
			return nil, err
		}
		eventos = append(eventos, e)
	}
	return eventos, linhas.Err()
}

// UltimoEvento é o id do evento mais recente (0 sem eventos).
func (s *Servidor) UltimoEvento() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT coalesce(max(id), 0) FROM eventos`).Scan(&n)
	return n, err
}

// Notificacao monta o corpo de um gancho: o evento e o estado atual da entidade dele, quando é um
// lembrete, uma tarefa ou uma sessão.
func (s *Servidor) Notificacao(e api.Evento) (api.Notificacao, error) {
	n := api.Notificacao{Evento: e}
	prefixo, _, _ := strings.Cut(e.Entidade, "-")
	var err error
	switch prefixo {
	case "L":
		var x api.Lembrete
		if x, err = s.carregarLembrete(s.db, mustNumero(e.Entidade, "L")); err == nil {
			n.Lembrete = &x
		}
	case "T":
		var x api.Tarefa
		if x, err = carregarTarefa(s.db, mustNumero(e.Entidade, "T")); err == nil {
			n.Tarefa = &x
		}
	case "S":
		var x api.Sessao
		if x, err = carregarSessao(s.db, mustNumero(e.Entidade, "S")); err == nil {
			n.Sessao = &x
		}
	}
	var h *erroHTTP
	if errors.As(err, &h) && h.status == http.StatusNotFound {
		err = nil
	}
	return n, err
}

// Cursor e GravarCursor guardam até que evento cada gancho já entregou. Sem cursor, ok é falso.
func (s *Servidor) Cursor(gancho string) (evento int64, ok bool, err error) {
	err = s.db.QueryRow(`SELECT evento FROM cursores_de_integracao WHERE gancho = ?`, gancho).Scan(&evento)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return evento, err == nil, err
}

func (s *Servidor) GravarCursor(gancho string, evento int64) error {
	_, err := s.db.Exec(`INSERT INTO cursores_de_integracao (gancho, evento) VALUES (?, ?)
		ON CONFLICT (gancho) DO UPDATE SET evento = excluded.evento`, gancho, evento)
	return err
}

// RegistrarFalhaDeIntegracao grava no log que um gancho desistiu de um evento.
func (s *Servidor) RegistrarFalhaDeIntegracao(gancho string, evento int64, erro string) error {
	return s.emTransacao(func(tx *sql.Tx) error {
		return s.registrarEvento(tx, "integracao", "gancho:"+gancho, "integracao_falhou", map[string]any{"evento": evento, "erro": erro})
	})
}
