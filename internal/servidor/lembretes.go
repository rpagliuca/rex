package servidor

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rpagliuca/rex/api"
)

func idLembrete(n int64) string { return idCom("L", n) }

// toleranciaDeDisparo: um disparo até este tempo depois da hora não conta como atrasado.
const toleranciaDeDisparo = time.Minute

const colunasLembrete = `id, texto, curto, quando, precisao, organizacao, tarefa_id, requisito_id, status, autor, disparado_em, atrasado,
	adiamentos, confirmado_em, criado_em, atualizado_em`

// pendente: os status que ainda pedem atenção.
var statusPendentes = []api.StatusLembrete{api.Agendado, api.Adiado, api.Disparado}

func (s *Servidor) lerLembrete(linha varredor) (api.Lembrete, error) {
	var x api.Lembrete
	var n int64
	var quando, precisao, status, criado, atualizado string
	var tarefa, requisito sql.NullInt64
	var curto, disparado, confirmado sql.NullString
	var atrasado bool
	if err := linha.Scan(&n, &x.Texto, &curto, &quando, &precisao, &x.Organizacao, &tarefa, &requisito, &status, &x.Autor, &disparado,
		&atrasado, &x.Adiamentos, &confirmado, &criado, &atualizado); err != nil {
		return x, err
	}
	x.Id = idLembrete(n)
	if curto.Valid {
		x.Curto = &curto.String
	}
	x.Quando, _ = time.Parse(time.RFC3339, quando)
	x.Precisao = api.PrecisaoLembrete(precisao)
	if x.Precisao != api.PrecisaoHora {
		_, fim := s.turnos().janela(x.Precisao, x.Quando)
		x.Fim = &fim
	}
	x.Status = api.StatusLembrete(status)
	x.Atrasado = &atrasado
	x.CriadoEm = lerMomento(criado)
	x.AtualizadoEm = lerMomento(atualizado)
	if tarefa.Valid {
		id := idTarefa(tarefa.Int64)
		x.Tarefa = &id
	}
	if requisito.Valid {
		id := idRequisito(requisito.Int64)
		x.Requisito = &id
	}
	if disparado.Valid {
		t := lerMomento(disparado.String)
		x.DisparadoEm = &t
	}
	if confirmado.Valid {
		t := lerMomento(confirmado.String)
		x.ConfirmadoEm = &t
	}
	if s.integracao.BasePublica != "" {
		link := strings.TrimRight(s.integracao.BasePublica, "/") + "/#/lembretes/" + x.Id
		x.Link = &link
	}
	return x, nil
}

func (s *Servidor) carregarLembrete(c consultor, n int64) (api.Lembrete, error) {
	x, err := s.lerLembrete(c.QueryRow(`SELECT `+colunasLembrete+` FROM lembretes WHERE id = ?`, n))
	if errors.Is(err, sql.ErrNoRows) {
		return x, falha(http.StatusNotFound, "lembrete %s não encontrado", idLembrete(n))
	}
	return x, err
}

// listarLembretes devolve os que casam com o filtro, por hora marcada.
func (s *Servidor) listarLembretes(c consultor, filtro string, args ...any) ([]api.Lembrete, error) {
	linhas, err := c.Query(`SELECT `+colunasLembrete+` FROM lembretes WHERE `+filtro+` ORDER BY quando_utc, id`, args...)
	if err != nil {
		return nil, err
	}
	defer linhas.Close()
	lembretes := []api.Lembrete{}
	for linhas.Next() {
		x, err := s.lerLembrete(linhas)
		if err != nil {
			return nil, err
		}
		lembretes = append(lembretes, x)
	}
	return lembretes, linhas.Err()
}

func emSQL(status []api.StatusLembrete) (string, []any) {
	marcas := make([]string, len(status))
	args := make([]any, len(status))
	for i, st := range status {
		marcas[i], args[i] = "?", string(st)
	}
	return "status IN (" + strings.Join(marcas, ", ") + ")", args
}

func statusDeLembreteValido(st api.StatusLembrete) bool {
	switch st {
	case api.Agendado, api.Disparado, api.Adiado, api.Confirmado, api.Cancelado:
		return true
	}
	return false
}

func utc(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// ligacoesDoLembrete resolve tarefa e requisito opcionais em números (nil quando vazios).
func ligacoesDoLembrete(tx *sql.Tx, tarefa, requisito *string) (any, any, error) {
	var t, r any
	if tarefa != nil && !vazio(*tarefa) {
		n, err := numero(*tarefa, "T")
		if err != nil {
			return nil, nil, err
		}
		if _, err := carregarTarefa(tx, n); err != nil {
			return nil, nil, err
		}
		t = n
	}
	if requisito != nil && !vazio(*requisito) {
		n, err := numero(*requisito, "R")
		if err != nil {
			return nil, nil, err
		}
		if err := existeRequisito(tx, n); err != nil {
			return nil, nil, err
		}
		r = n
	}
	return t, r, nil
}

func (s *Servidor) ListarLembretes(w http.ResponseWriter, r *http.Request, params api.ListarLembretesParams) {
	status := statusPendentes
	if params.Status != nil {
		status = nil
		for _, st := range strings.Split(*params.Status, ",") {
			st := api.StatusLembrete(strings.TrimSpace(st))
			if !statusDeLembreteValido(st) {
				responderErro(w, falha(http.StatusBadRequest, "status de lembrete inválido: %q", st))
				return
			}
			status = append(status, st)
		}
	}
	filtro, args := emSQL(status)
	if params.Ate != nil {
		filtro += ` AND quando_utc <= ?`
		args = append(args, utc(*params.Ate))
	}
	if params.Organizacao != nil {
		filtro += ` AND organizacao = ?`
		args = append(args, *params.Organizacao)
	}
	if params.Tarefa != nil {
		n, err := numero(*params.Tarefa, "T")
		if err != nil {
			responderErro(w, err)
			return
		}
		filtro += ` AND tarefa_id = ?`
		args = append(args, n)
	}
	lembretes, err := s.listarLembretes(s.db, filtro, args...)
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusOK, lembretes)
}

func (s *Servidor) CriarLembrete(w http.ResponseWriter, r *http.Request) {
	var novo api.NovoLembrete
	if err := ler(r, &novo); err != nil {
		responderErro(w, err)
		return
	}
	var criado api.Lembrete
	err := s.emTransacao(func(tx *sql.Tx) error {
		quem, err := autor(tx, r)
		if err != nil {
			return err
		}
		precisao := api.PrecisaoHora
		if novo.Precisao != nil {
			precisao = *novo.Precisao
		}
		if novo.Quando, err = s.marcar(precisao, novo.Quando); err != nil {
			return err
		}
		novo.Precisao = &precisao
		criado, err = s.inserirLembrete(tx, quem, novo)
		return err
	})
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusCreated, criado)
}

// maxCurto: o resumo da ilha do iPhone mostra "16:50 " + a palavra-chave num valor de 16 caracteres.
const maxCurto = 10

// curtoDoLembrete: a palavra-chave aparada; vazia (ou ausente) vira NULL.
func curtoDoLembrete(c *string) (any, error) {
	if c == nil || vazio(*c) {
		return nil, nil
	}
	v := strings.TrimSpace(*c)
	if n := utf8.RuneCountInString(v); n > maxCurto {
		return nil, falha(http.StatusBadRequest, "curto tem no máximo %d caracteres (%q tem %d)", maxCurto, v, n)
	}
	return v, nil
}

// inserirLembrete grava um lembrete agendado e o evento; não confere se a hora já passou (o
// agendador o dispara como atrasado).
func (s *Servidor) inserirLembrete(tx *sql.Tx, quem string, novo api.NovoLembrete) (api.Lembrete, error) {
	if vazio(novo.Texto) {
		return api.Lembrete{}, falha(http.StatusBadRequest, "o texto é obrigatório")
	}
	if vazio(novo.Organizacao) {
		return api.Lembrete{}, falha(http.StatusBadRequest, "a organização é obrigatória")
	}
	if novo.Quando.IsZero() {
		return api.Lembrete{}, falha(http.StatusBadRequest, "a hora marcada é obrigatória")
	}
	precisao := api.PrecisaoHora
	if novo.Precisao != nil {
		precisao = *novo.Precisao
	}
	curto, err := curtoDoLembrete(novo.Curto)
	if err != nil {
		return api.Lembrete{}, err
	}
	tarefa, requisito, err := ligacoesDoLembrete(tx, novo.Tarefa, novo.Requisito)
	if err != nil {
		return api.Lembrete{}, err
	}
	agora := s.momento()
	res, err := tx.Exec(`INSERT INTO lembretes (texto, curto, quando, quando_utc, precisao, organizacao, tarefa_id, requisito_id, autor, criado_em, atualizado_em)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, strings.TrimSpace(novo.Texto), curto, novo.Quando.Format(time.RFC3339), utc(novo.Quando),
		string(precisao), strings.TrimSpace(novo.Organizacao), tarefa, requisito, quem, agora, agora)
	if err != nil {
		return api.Lembrete{}, err
	}
	n, err := res.LastInsertId()
	if err != nil {
		return api.Lembrete{}, err
	}
	criado, err := s.carregarLembrete(tx, n)
	if err != nil {
		return criado, err
	}
	return criado, s.registrarEvento(tx, quem, criado.Id, "lembrete_criado", map[string]any{"quando": criado.Quando, "precisao": criado.Precisao})
}

func (s *Servidor) ObterLembrete(w http.ResponseWriter, r *http.Request, id api.Id) {
	n, err := numero(id, "L")
	if err != nil {
		responderErro(w, err)
		return
	}
	x, err := s.carregarLembrete(s.db, n)
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusOK, x)
}

// operarLembrete carrega o lembrete numa transação, recusa os finais e aplica f.
func (s *Servidor) operarLembrete(w http.ResponseWriter, r *http.Request, id api.Id, corpo any,
	f func(tx *sql.Tx, quem string, n int64, atual api.Lembrete) error) {
	n, err := numero(id, "L")
	if err != nil {
		responderErro(w, err)
		return
	}
	if corpo != nil {
		if err := ler(r, corpo); err != nil {
			responderErro(w, err)
			return
		}
	}
	var x api.Lembrete
	err = s.emTransacao(func(tx *sql.Tx) error {
		quem, err := autor(tx, r)
		if err != nil {
			return err
		}
		atual, err := s.carregarLembrete(tx, n)
		if err != nil {
			return err
		}
		if err := f(tx, quem, n, atual); err != nil {
			return err
		}
		x, err = s.carregarLembrete(tx, n)
		return err
	})
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusOK, x)
}

func final(x api.Lembrete) error {
	if x.Status == api.Confirmado || x.Status == api.Cancelado {
		return falha(http.StatusConflict, "o lembrete %s já está %s", x.Id, x.Status)
	}
	return nil
}

func (s *Servidor) AlterarLembrete(w http.ResponseWriter, r *http.Request, id api.Id) {
	var alteracao api.AlteracaoLembrete
	s.operarLembrete(w, r, id, &alteracao, func(tx *sql.Tx, quem string, n int64, atual api.Lembrete) error {
		if err := final(atual); err != nil {
			return err
		}
		campos := map[string]any{}
		var sets []string
		var args []any
		definir := func(coluna string, valor any) {
			sets = append(sets, coluna+" = ?")
			args = append(args, valor)
			campos[coluna] = valor
		}
		if alteracao.Texto != nil {
			if vazio(*alteracao.Texto) {
				return falha(http.StatusBadRequest, "o texto não pode ficar vazio")
			}
			definir("texto", strings.TrimSpace(*alteracao.Texto))
		}
		if alteracao.Curto != nil {
			curto, err := curtoDoLembrete(alteracao.Curto)
			if err != nil {
				return err
			}
			definir("curto", curto)
		}
		if alteracao.Organizacao != nil {
			if vazio(*alteracao.Organizacao) {
				return falha(http.StatusBadRequest, "a organização não pode ficar vazia")
			}
			definir("organizacao", strings.TrimSpace(*alteracao.Organizacao))
		}
		if alteracao.Quando != nil || alteracao.Precisao != nil {
			precisao, quando := atual.Precisao, atual.Quando
			if alteracao.Precisao != nil {
				precisao = *alteracao.Precisao
				definir("precisao", string(precisao))
			}
			if alteracao.Quando != nil {
				quando = *alteracao.Quando
			}
			quando, err := s.marcar(precisao, quando)
			if err != nil {
				return err
			}
			if alteracao.Quando != nil || !quando.Equal(atual.Quando) {
				definir("quando", quando.Format(time.RFC3339))
				sets, args = append(sets, "quando_utc = ?"), append(args, utc(quando))
				if atual.Status == api.Disparado {
					definir("status", string(api.Agendado))
				}
			}
		}
		if alteracao.Tarefa != nil || alteracao.Requisito != nil {
			tarefa, requisito, err := ligacoesDoLembrete(tx, alteracao.Tarefa, alteracao.Requisito)
			if err != nil {
				return err
			}
			if alteracao.Tarefa != nil {
				definir("tarefa_id", tarefa)
			}
			if alteracao.Requisito != nil {
				definir("requisito_id", requisito)
			}
		}
		if len(sets) == 0 {
			return falha(http.StatusBadRequest, "nenhum campo para alterar")
		}
		sets = append(sets, "atualizado_em = ?")
		args = append(args, s.momento(), n)
		if _, err := tx.Exec(`UPDATE lembretes SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
			return err
		}
		return s.registrarEvento(tx, quem, atual.Id, "lembrete_alterado", campos)
	})
}

func (s *Servidor) ConfirmarLembrete(w http.ResponseWriter, r *http.Request, id api.Id) {
	s.operarLembrete(w, r, id, nil, func(tx *sql.Tx, quem string, n int64, atual api.Lembrete) error {
		if atual.Status == api.Confirmado {
			return nil
		}
		if err := final(atual); err != nil {
			return err
		}
		agora := s.momento()
		if _, err := tx.Exec(`UPDATE lembretes SET status = ?, confirmado_em = ?, atualizado_em = ? WHERE id = ?`,
			string(api.Confirmado), agora, agora, n); err != nil {
			return err
		}
		return s.registrarEvento(tx, quem, atual.Id, "lembrete_confirmado", nil)
	})
}

func (s *Servidor) AdiarLembrete(w http.ResponseWriter, r *http.Request, id api.Id) {
	var adiamento api.Adiamento
	s.operarLembrete(w, r, id, &adiamento, func(tx *sql.Tx, quem string, n int64, atual api.Lembrete) error {
		if err := final(atual); err != nil {
			return err
		}
		var quando time.Time
		switch {
		case adiamento.Ate != nil && adiamento.Minutos != nil:
			return falha(http.StatusBadRequest, "informe ate ou minutos, não os dois")
		case adiamento.Ate != nil:
			quando = *adiamento.Ate
		case adiamento.Minutos != nil && *adiamento.Minutos >= 1:
			// Mantém o fuso em que o lembrete foi pedido.
			quando = s.agora().Add(time.Duration(*adiamento.Minutos) * time.Minute).In(atual.Quando.Location()).Truncate(time.Second)
		default:
			return falha(http.StatusBadRequest, "informe ate ou minutos (pelo menos 1)")
		}
		if !quando.After(s.agora()) {
			return falha(http.StatusBadRequest, "a nova hora precisa estar no futuro: %s", quando.Format(time.RFC3339))
		}
		if _, err := tx.Exec(`UPDATE lembretes SET status = ?, quando = ?, quando_utc = ?, adiamentos = adiamentos + 1, atualizado_em = ? WHERE id = ?`,
			string(api.Adiado), quando.Format(time.RFC3339), utc(quando), s.momento(), n); err != nil {
			return err
		}
		return s.registrarEvento(tx, quem, atual.Id, "lembrete_adiado", map[string]any{"quando": quando.Format(time.RFC3339)})
	})
}

func (s *Servidor) CancelarLembrete(w http.ResponseWriter, r *http.Request, id api.Id) {
	s.operarLembrete(w, r, id, nil, func(tx *sql.Tx, quem string, n int64, atual api.Lembrete) error {
		if err := final(atual); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE lembretes SET status = ?, atualizado_em = ? WHERE id = ?`,
			string(api.Cancelado), s.momento(), n); err != nil {
			return err
		}
		return s.registrarEvento(tx, quem, atual.Id, "lembrete_cancelado", nil)
	})
}

// DispararVencidos marca como disparados os lembretes agendados ou adiados cuja hora chegou, com
// um evento cada; os que venceram há mais que a tolerância (servidor parado) saem como atrasados.
// O agendador chama periodicamente; devolve quantos disparou.
func (s *Servidor) DispararVencidos() (int, error) {
	disparados := 0
	err := s.emTransacao(func(tx *sql.Tx) error {
		agora := s.agora()
		filtro, args := emSQL([]api.StatusLembrete{api.Agendado, api.Adiado})
		vencidos, err := s.listarLembretes(tx, filtro+` AND quando_utc <= ?`, append(args, utc(agora))...)
		if err != nil {
			return err
		}
		for _, x := range vencidos {
			// Turno e dia valem até o fim da janela: disparar depois do início não é atraso.
			atrasado := x.Precisao == api.PrecisaoHora && agora.Sub(x.Quando) > toleranciaDeDisparo
			if _, err := tx.Exec(`UPDATE lembretes SET status = ?, disparado_em = ?, atrasado = ?, atualizado_em = ? WHERE id = ?`,
				string(api.Disparado), s.momento(), atrasado, s.momento(), mustNumero(x.Id, "L")); err != nil {
				return err
			}
			if err := s.registrarEvento(tx, "agendador", x.Id, "lembrete_disparado", map[string]any{
				"quando": x.Quando.Format(time.RFC3339), "atrasado": atrasado,
			}); err != nil {
				return err
			}
		}
		disparados = len(vencidos)
		return nil
	})
	return disparados, err
}
