package servidor

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rpagliuca/rex/api"
)

func idTemplate(n int64) string { return idCom("TP", n) }

// instanciasDosTemplates: por template, as instâncias da mais recente para a mais antiga.
type instancia struct {
	id     string
	status api.StatusTarefa
	criada time.Time
}

func carregarInstancias(c consultor) (map[int64][]instancia, map[int64][]string, error) {
	inst := map[int64][]instancia{}
	linhas, err := c.Query(`SELECT template_id, id, status, criada_em FROM tarefas WHERE template_id IS NOT NULL ORDER BY criada_em DESC, id DESC`)
	if err != nil {
		return nil, nil, err
	}
	for linhas.Next() {
		var tp, n int64
		var status, criada string
		if err := linhas.Scan(&tp, &n, &status, &criada); err != nil {
			linhas.Close()
			return nil, nil, err
		}
		inst[tp] = append(inst[tp], instancia{idTarefa(n), api.StatusTarefa(status), lerMomento(criada)})
	}
	if err := linhas.Close(); err != nil {
		return nil, nil, err
	}
	reqs := map[int64][]string{}
	linhas, err = c.Query(`SELECT template_id, requisito_id FROM template_requisito ORDER BY requisito_id`)
	if err != nil {
		return nil, nil, err
	}
	defer linhas.Close()
	for linhas.Next() {
		var tp, r int64
		if err := linhas.Scan(&tp, &r); err != nil {
			return nil, nil, err
		}
		reqs[tp] = append(reqs[tp], idRequisito(r))
	}
	return inst, reqs, linhas.Err()
}

const colunasTemplate = `id, titulo, descricao, organizacao, periodicidade_dias, criado_em, atualizado_em`

func lerTemplate(linha varredor, inst map[int64][]instancia, reqs map[int64][]string, agora time.Time) (api.Template, error) {
	var x api.Template
	var n int64
	var periodicidade sql.NullInt64
	var criado, atualizado string
	if err := linha.Scan(&n, &x.Titulo, &x.Descricao, &x.Organizacao, &periodicidade, &criado, &atualizado); err != nil {
		return x, err
	}
	x.Id, x.CriadoEm, x.AtualizadoEm = idTemplate(n), lerMomento(criado), lerMomento(atualizado)
	x.Requisitos = naoNulo(reqs[n])
	x.Instancias = []string{}
	for _, i := range inst[n] {
		x.Instancias = append(x.Instancias, i.id)
		if i.status != api.Concluida && i.status != api.Cancelada && x.InstanciaAberta == nil {
			id := i.id
			x.InstanciaAberta = &id
		}
	}
	if len(inst[n]) > 0 {
		ultima := inst[n][0].criada
		x.UltimaInstanciaEm = &ultima
	}
	if periodicidade.Valid {
		p := int(periodicidade.Int64)
		x.PeriodicidadeDias = &p
		proxima := x.CriadoEm
		if x.UltimaInstanciaEm != nil {
			proxima = x.UltimaInstanciaEm.AddDate(0, 0, p)
		}
		x.ProximaEm = &proxima
		x.Devido = x.InstanciaAberta == nil && !proxima.After(agora)
	}
	return x, nil
}

func (s *Servidor) carregarTemplate(c consultor, n int64) (api.Template, error) {
	inst, reqs, err := carregarInstancias(c)
	if err != nil {
		return api.Template{}, err
	}
	x, err := lerTemplate(c.QueryRow(`SELECT `+colunasTemplate+` FROM templates WHERE id = ?`, n), inst, reqs, s.agora())
	if errors.Is(err, sql.ErrNoRows) {
		return x, falha(http.StatusNotFound, "template %s não encontrado", idTemplate(n))
	}
	return x, err
}

func (s *Servidor) ListarTemplates(w http.ResponseWriter, r *http.Request) {
	inst, reqs, err := carregarInstancias(s.db)
	if err != nil {
		responderErro(w, err)
		return
	}
	linhas, err := s.db.Query(`SELECT ` + colunasTemplate + ` FROM templates ORDER BY titulo`)
	if err != nil {
		responderErro(w, err)
		return
	}
	defer linhas.Close()
	templates := []api.Template{}
	for linhas.Next() {
		x, err := lerTemplate(linhas, inst, reqs, s.agora())
		if err != nil {
			responderErro(w, err)
			return
		}
		templates = append(templates, x)
	}
	if err := linhas.Err(); err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusOK, templates)
}

// gravarRequisitosDoTemplate substitui a lista de requisitos do template.
func gravarRequisitosDoTemplate(tx *sql.Tx, n int64, ids []string) error {
	if _, err := tx.Exec(`DELETE FROM template_requisito WHERE template_id = ?`, n); err != nil {
		return err
	}
	for _, id := range ids {
		r, err := numero(id, "R")
		if err != nil {
			return err
		}
		if err := existeRequisito(tx, r); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO template_requisito (template_id, requisito_id) VALUES (?, ?)`, n, r); err != nil {
			return err
		}
	}
	return nil
}

func periodicidade(dias *int, zeroRemove bool) (any, error) {
	switch {
	case dias == nil:
		return nil, nil
	case *dias == 0 && zeroRemove:
		return nil, nil
	case *dias < 1:
		return nil, falha(http.StatusBadRequest, "a periodicidade é de pelo menos 1 dia")
	}
	return *dias, nil
}

func (s *Servidor) CriarTemplate(w http.ResponseWriter, r *http.Request) {
	var novo api.NovoTemplate
	if err := ler(r, &novo); err != nil {
		responderErro(w, err)
		return
	}
	var criado api.Template
	err := s.emTransacao(func(tx *sql.Tx) error {
		quem, err := autor(tx, r)
		if err != nil {
			return err
		}
		if vazio(novo.Titulo) || vazio(novo.Organizacao) {
			return falha(http.StatusBadRequest, "título e organização são obrigatórios")
		}
		dias, err := periodicidade(novo.PeriodicidadeDias, false)
		if err != nil {
			return err
		}
		descricao := ""
		if novo.Descricao != nil {
			descricao = *novo.Descricao
		}
		agora := s.momento()
		res, err := tx.Exec(`INSERT INTO templates (titulo, descricao, organizacao, periodicidade_dias, criado_em, atualizado_em)
			VALUES (?, ?, ?, ?, ?, ?)`, strings.TrimSpace(novo.Titulo), descricao, strings.TrimSpace(novo.Organizacao), dias, agora, agora)
		if err != nil {
			return err
		}
		n, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if novo.Requisitos != nil {
			if err := gravarRequisitosDoTemplate(tx, n, *novo.Requisitos); err != nil {
				return err
			}
		}
		if criado, err = s.carregarTemplate(tx, n); err != nil {
			return err
		}
		return s.registrarEvento(tx, quem, criado.Id, "template_criado", map[string]any{"titulo": criado.Titulo})
	})
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusCreated, criado)
}

func (s *Servidor) ObterTemplate(w http.ResponseWriter, r *http.Request, id api.Id) {
	n, err := numero(id, "TP")
	if err != nil {
		responderErro(w, err)
		return
	}
	x, err := s.carregarTemplate(s.db, n)
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusOK, x)
}

func (s *Servidor) AlterarTemplate(w http.ResponseWriter, r *http.Request, id api.Id) {
	n, err := numero(id, "TP")
	if err != nil {
		responderErro(w, err)
		return
	}
	var alteracao api.AlteracaoTemplate
	if err := ler(r, &alteracao); err != nil {
		responderErro(w, err)
		return
	}
	var alterado api.Template
	err = s.emTransacao(func(tx *sql.Tx) error {
		quem, err := autor(tx, r)
		if err != nil {
			return err
		}
		if _, err := s.carregarTemplate(tx, n); err != nil {
			return err
		}
		campos := map[string]any{}
		for coluna, valor := range map[string]*string{"titulo": alteracao.Titulo, "organizacao": alteracao.Organizacao} {
			if valor != nil {
				if vazio(*valor) {
					return falha(http.StatusBadRequest, "%s não pode ficar vazio", coluna)
				}
				campos[coluna] = strings.TrimSpace(*valor)
			}
		}
		if alteracao.Descricao != nil {
			campos["descricao"] = *alteracao.Descricao
		}
		if alteracao.PeriodicidadeDias != nil {
			if campos["periodicidade_dias"], err = periodicidade(alteracao.PeriodicidadeDias, true); err != nil {
				return err
			}
		}
		if len(campos) == 0 && alteracao.Requisitos == nil {
			return falha(http.StatusBadRequest, "nenhum campo para alterar")
		}
		campos["atualizado_em"] = s.momento()
		for coluna, valor := range campos {
			if _, err := tx.Exec(`UPDATE templates SET `+coluna+` = ? WHERE id = ?`, valor, n); err != nil {
				return err
			}
		}
		if alteracao.Requisitos != nil {
			if err := gravarRequisitosDoTemplate(tx, n, *alteracao.Requisitos); err != nil {
				return err
			}
			campos["requisitos"] = *alteracao.Requisitos
		}
		if alterado, err = s.carregarTemplate(tx, n); err != nil {
			return err
		}
		delete(campos, "atualizado_em")
		return s.registrarEvento(tx, quem, alterado.Id, "template_alterado", campos)
	})
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusOK, alterado)
}

func (s *Servidor) InstanciarTemplate(w http.ResponseWriter, r *http.Request, id api.Id) {
	n, err := numero(id, "TP")
	if err != nil {
		responderErro(w, err)
		return
	}
	var pedido api.PedidoDeInstancia
	if err := ler(r, &pedido); err != nil {
		responderErro(w, err)
		return
	}
	var criada api.Tarefa
	var quem string
	err = s.emTransacao(func(tx *sql.Tx) (err error) {
		if quem, err = autor(tx, r); err != nil {
			return err
		}
		tp, err := s.carregarTemplate(tx, n)
		if err != nil {
			return err
		}
		titulo := fmt.Sprintf("%s — %s", tp.Titulo, s.agora().Local().Format("2006-01-02"))
		if pedido.Titulo != nil && !vazio(*pedido.Titulo) {
			titulo = strings.TrimSpace(*pedido.Titulo)
		}
		descricao := tp.Descricao
		if pedido.Contexto != nil && !vazio(*pedido.Contexto) {
			descricao = "## Contexto desta instância\n\n" + strings.TrimSpace(*pedido.Contexto) +
				"\n\nAo começar, dê a esta tarefa um título específico a partir do contexto (alterar_tarefa).\n\n## Instruções do template\n\n" + tp.Descricao
		}
		if criada, err = s.inserirTarefa(tx, quem, titulo, descricao, tp.Organizacao, api.AFazer, n); err != nil {
			return err
		}
		for _, rid := range tp.Requisitos {
			r, _ := numero(rid, "R")
			if _, err := tx.Exec(`INSERT INTO tarefa_requisito (tarefa_id, requisito_id) VALUES (?, ?)`, mustNumero(criada.Id, "T"), r); err != nil {
				return err
			}
		}
		criada, err = carregarTarefa(tx, mustNumero(criada.Id, "T"))
		return err
	})
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusCreated, criada)
}

func mustNumero(id, prefixo string) int64 {
	n, _ := numero(id, prefixo)
	return n
}
