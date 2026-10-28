package servidor

import (
	"database/sql"
	"net/http"

	"github.com/rpagliuca/rex/api"
)

func (s *Servidor) OpcoesDoLancador(w http.ResponseWriter, r *http.Request) {
	if s.lancador == nil {
		responderErro(w, falha(http.StatusNotFound, "o lançador de agentes não está configurado"))
		return
	}
	responder(w, http.StatusOK, s.lancador.Opcoes())
}

// LancarAgente põe a tarefa em andamento e pede um agente com as opções dadas. Serve também para
// relançar uma tarefa que ficou em andamento sem agente.
func (s *Servidor) LancarAgente(w http.ResponseWriter, r *http.Request, id api.Id) {
	n, err := numero(id, "T")
	if err != nil {
		responderErro(w, err)
		return
	}
	var pedido api.PedidoDeLancamento
	if err := ler(r, &pedido); err != nil {
		responderErro(w, err)
		return
	}
	if s.lancador == nil {
		responderErro(w, falha(http.StatusNotFound, "o lançador de agentes não está configurado"))
		return
	}
	if err := s.lancador.Validar(&pedido); err != nil {
		responderErro(w, falha(http.StatusBadRequest, "%v", err))
		return
	}
	if err := s.conferirMemoria(r.Context(), pedido.Forcar != nil && *pedido.Forcar); err != nil {
		responderErro(w, err)
		return
	}
	tarefa, err := s.pedirAgente(n, pedido, func(tx *sql.Tx) (string, error) { return autor(tx, r) })
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusOK, tarefa)
}

// pedirAgente põe a tarefa em andamento, registra o pedido e avisa o lançador. O pedido já vem
// validado.
func (s *Servidor) pedirAgente(n int64, pedido api.PedidoDeLancamento, quemPede func(*sql.Tx) (string, error)) (api.Tarefa, error) {
	var tarefa api.Tarefa
	var anterior api.StatusTarefa
	var quem string
	err := s.emTransacao(func(tx *sql.Tx) (err error) {
		if quem, err = quemPede(tx); err != nil {
			return err
		}
		atual, err := carregarTarefa(tx, n)
		if err != nil {
			return err
		}
		if atual.SessaoAtiva != nil {
			return falha(http.StatusConflict, "a tarefa %s já tem a sessão ativa %s", atual.Id, *atual.SessaoAtiva)
		}
		anterior = atual.Status
		if anterior != api.EmAndamento {
			if _, err := tx.Exec(`UPDATE tarefas SET status = ?, atualizada_em = ? WHERE id = ?`,
				string(api.EmAndamento), s.momento(), n); err != nil {
				return err
			}
			if err := s.registrarEvento(tx, quem, atual.Id, "tarefa_alterada", map[string]any{"status": api.EmAndamento}); err != nil {
				return err
			}
		}
		dados := map[string]any{"modelo": pedido.Modelo, "esforco": pedido.Esforco}
		if pedido.Nome != nil {
			dados["nome"] = *pedido.Nome
		}
		if pedido.Instrucao != nil && !vazio(*pedido.Instrucao) {
			dados["instrucao"] = *pedido.Instrucao
		}
		if err := s.registrarEvento(tx, quem, atual.Id, "agente_pedido", dados); err != nil {
			return err
		}
		tarefa, err = carregarTarefa(tx, n)
		return err
	})
	if err != nil {
		return tarefa, err
	}
	s.avisarMudanca(MudancaDeStatus{Tarefa: tarefa, Anterior: anterior, Autor: quem, Lancamento: &pedido})
	return tarefa, nil
}

// autorLancador assina os eventos das sessões abertas pelo lançador.
const autorLancador = "lancador"

// AbrirSessaoLancada abre a sessão de um agente que o lançador vai subir: quem lança já sabe a
// tarefa, o agente e o chat, então a sessão existe antes do agente. O token vai para o agente pelo
// ambiente; o nome é o do tmux em que ele vai rodar.
func (s *Servidor) AbrirSessaoLancada(tarefa, agente, servidor, chat, nome string) (api.SessaoCriada, error) {
	var criada api.SessaoCriada
	t, err := numero(tarefa, "T")
	if err != nil {
		return criada, err
	}
	err = s.emTransacao(func(tx *sql.Tx) error {
		if err := validarAgente(agente, api.Ia); err != nil {
			return err
		}
		n, token, err := s.iniciar(tx, agente, api.Ia, &chat, servidor, nil, nil, []int64{t})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE sessoes SET nome = ? WHERE id = ?`, nome, n); err != nil {
			return err
		}
		if criada.Sessao, err = carregarSessao(tx, n); err != nil {
			return err
		}
		criada.Token = token
		return s.registrarEvento(tx, autorLancador, criada.Sessao.Id, "sessao_iniciada", map[string]any{
			"agente": agente, "tipo_agente": api.Ia, "servidor": servidor, "tarefas": criada.Sessao.Tarefas, "chat": chat, "nome": nome,
		})
	})
	return criada, err
}

// EncerrarSessaoLancada encerra a sessão de um lançamento que falhou antes de o agente subir.
func (s *Servidor) EncerrarSessaoLancada(id, resumo string) error {
	n, err := numero(id, "S")
	if err != nil {
		return err
	}
	var x api.Sessao
	err = s.emTransacao(func(tx *sql.Tx) error {
		if err := s.encerrarComResumo(tx, n, api.Encerramento{Resumo: resumo}, autorLancador); err != nil {
			return err
		}
		x, err = carregarSessao(tx, n)
		return err
	})
	if err == nil {
		s.avisarEncerramento(x)
	}
	return err
}

// Sessao lê uma sessão pelo identificador, para quem reage aos avisos fora de uma requisição.
func (s *Servidor) Sessao(id string) (api.Sessao, error) {
	n, err := numero(id, "S")
	if err != nil {
		return api.Sessao{}, err
	}
	return carregarSessao(s.db, n)
}
