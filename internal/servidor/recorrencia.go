package servidor

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/rpagliuca/rex/api"
)

// Recorrência de lembretes: cada ocorrência é um lembrete comum, e a série é a corrente ligada por
// seguinte_id. A próxima ocorrência sai da âncora (serie_inicio) mais (ocorrencia + 1) vezes o
// passo, então adiar uma ocorrência não desloca as seguintes.

func recorrenciaValida(r api.RecorrenciaLembrete) bool {
	switch r {
	case api.RecorrenciaDiaria, api.RecorrenciaSemanal, api.RecorrenciaMensal, api.RecorrenciaAnual:
		return true
	}
	return false
}

// largura ordena precisões e recorrências pelo tamanho da janela; a recorrência não pode ser mais
// curta que a janela (um lembrete de semana que se repete todo dia cairia sempre na mesma janela).
var largura = map[string]int{
	string(api.PrecisaoHora): 0, string(api.PrecisaoTurno): 1, string(api.PrecisaoDia): 2,
	string(api.PrecisaoSemana): 3, string(api.PrecisaoMes): 4,
	string(api.RecorrenciaDiaria): 2, string(api.RecorrenciaSemanal): 3, string(api.RecorrenciaMensal): 4, string(api.RecorrenciaAnual): 5,
}

func validarRecorrencia(r api.RecorrenciaLembrete, intervalo int, p api.PrecisaoLembrete) error {
	if !recorrenciaValida(r) {
		return falha(http.StatusBadRequest, "recorrência inválida: %q (diaria, semanal, mensal ou anual)", r)
	}
	if intervalo < 1 {
		return falha(http.StatusBadRequest, "o intervalo é pelo menos 1")
	}
	if largura[string(r)] < largura[string(p)] {
		return falha(http.StatusBadRequest, "um lembrete de precisão %s não pode se repetir com recorrência %s: cairia na mesma janela", p, r)
	}
	return nil
}

// avancar devolve a âncora mais n passos da recorrência, no fuso e na hora da âncora. No mensal e no
// anual, o dia que não existe no mês de destino (31, 29/2) vira o último dia do mês.
func avancar(ancora time.Time, r api.RecorrenciaLembrete, n int) time.Time {
	switch r {
	case api.RecorrenciaDiaria:
		return ancora.AddDate(0, 0, n)
	case api.RecorrenciaSemanal:
		return ancora.AddDate(0, 0, 7*n)
	}
	meses := n
	if r == api.RecorrenciaAnual {
		meses = 12 * n
	}
	primeiro := time.Date(ancora.Year(), ancora.Month(), 1, ancora.Hour(), ancora.Minute(), ancora.Second(), 0, ancora.Location()).AddDate(0, meses, 0)
	ultimo := primeiro.AddDate(0, 1, -1).Day()
	dia := min(ancora.Day(), ultimo)
	return primeiro.AddDate(0, 0, dia-1)
}

// serieDoLembrete: o que a tabela guarda da série de um lembrete, além do que api.Lembrete mostra.
type serieDoLembrete struct {
	recorrencia api.RecorrenciaLembrete
	intervalo   int
	inicio      time.Time
	ocorrencia  int
	serie       int64
	seguinte    sql.NullInt64
}

func lerSerie(c consultor, n int64) (serieDoLembrete, bool, error) {
	var s serieDoLembrete
	var rec, inicio sql.NullString
	var serie sql.NullInt64
	err := c.QueryRow(`SELECT recorrencia, intervalo, serie_inicio, ocorrencia, serie_id, seguinte_id FROM lembretes WHERE id = ?`, n).
		Scan(&rec, &s.intervalo, &inicio, &s.ocorrencia, &serie, &s.seguinte)
	if err != nil || !rec.Valid {
		return s, false, err
	}
	s.recorrencia = api.RecorrenciaLembrete(rec.String)
	s.inicio, err = time.Parse(time.RFC3339, inicio.String)
	s.serie = n
	if serie.Valid {
		s.serie = serie.Int64
	}
	return s, true, err
}

// gerarSeguinte cria a próxima ocorrência de um lembrete recorrente que ainda não a tem: a primeira
// depois da atual cuja janela não acabou (servidor parado não gera ocorrências já vencidas). O
// lembrete novo copia texto, curto, precisão, organização e ligações. Avulso ou já com seguinte:
// nada a fazer.
func (s *Servidor) gerarSeguinte(tx *sql.Tx, quem string, x api.Lembrete) error {
	n := mustNumero(x.Id, "L")
	serie, recorrente, err := lerSerie(tx, n)
	if err != nil || !recorrente || serie.seguinte.Valid {
		return err
	}
	agora := s.agora()
	k := serie.ocorrencia + 1
	var quando time.Time
	for {
		quando = avancar(serie.inicio, serie.recorrencia, k*serie.intervalo)
		inicio, fim := s.turnos().janela(x.Precisao, quando)
		quando = inicio
		if (x.Precisao == api.PrecisaoHora && quando.After(agora)) || (x.Precisao != api.PrecisaoHora && fim.After(agora)) {
			break
		}
		k++
	}
	var tarefa, requisito any
	if x.Tarefa != nil {
		tarefa = mustNumero(*x.Tarefa, "T")
	}
	if x.Requisito != nil {
		requisito = mustNumero(*x.Requisito, "R")
	}
	var curto any
	if x.Curto != nil {
		curto = *x.Curto
	}
	momento := s.momento()
	res, err := tx.Exec(`INSERT INTO lembretes (texto, curto, quando, quando_utc, precisao, organizacao, tarefa_id, requisito_id, autor,
		criado_em, atualizado_em, recorrencia, intervalo, serie_inicio, ocorrencia, serie_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, x.Texto, curto, quando.Format(time.RFC3339), utc(quando),
		string(x.Precisao), x.Organizacao, tarefa, requisito, quem, momento, momento,
		string(serie.recorrencia), serie.intervalo, serie.inicio.Format(time.RFC3339), k, serie.serie)
	if err != nil {
		return err
	}
	novo, err := res.LastInsertId()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE lembretes SET seguinte_id = ? WHERE id = ?`, novo, n); err != nil {
		return err
	}
	return s.registrarEvento(tx, quem, idLembrete(novo), "lembrete_criado", map[string]any{
		"quando": quando.Format(time.RFC3339), "precisao": x.Precisao, "serie": idLembrete(serie.serie), "anterior": x.Id,
	})
}
