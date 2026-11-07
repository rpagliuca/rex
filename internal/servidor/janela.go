package servidor

import (
	"fmt"
	"net/http"
	"time"

	"github.com/rpagliuca/rex/api"
)

// Turnos são as horas locais em que começam a manhã, o almoço, a tarde e a noite; a noite vai até
// a meia-noite e o dia começa com a manhã. Valores em minutos desde a meia-noite.
type Turnos struct {
	Manha, Almoco, Tarde, Noite int
}

// TurnosPadrao: manhã às 8h, almoço às 12h, tarde às 13h, noite às 18h.
var TurnosPadrao = Turnos{Manha: 8 * 60, Almoco: 12 * 60, Tarde: 13 * 60, Noite: 18 * 60}

// Validar confere a ordem manhã < almoço < tarde < noite, dentro do dia.
func (t Turnos) Validar() error {
	if !(0 <= t.Manha && t.Manha < t.Almoco && t.Almoco < t.Tarde && t.Tarde < t.Noite && t.Noite < 24*60) {
		return fmt.Errorf("os turnos precisam começar em ordem dentro do dia (manhã < almoço < tarde < noite)")
	}
	return nil
}

func (s *Servidor) turnos() Turnos {
	if s.integracao.Turnos == (Turnos{}) {
		return TurnosPadrao
	}
	return s.integracao.Turnos
}

func precisaoValida(p api.PrecisaoLembrete) bool {
	switch p {
	case api.PrecisaoHora, api.PrecisaoTurno, api.PrecisaoDia, api.PrecisaoSemana, api.PrecisaoFimDeSemana, api.PrecisaoMes:
		return true
	}
	return false
}

// janela devolve início e fim (exclusivo) da janela de quando, no fuso do próprio quando:
//   - turno: o turno que contém a hora (antes da manhã conta como manhã);
//   - dia: da manhã até a meia-noite;
//   - semana: de segunda de manhã até sexta no início da noite (18h no padrão); sexta à noite, sábado
//     e domingo já são da semana seguinte;
//   - fim_de_semana: de sábado de manhã até a meia-noite de domingo; de segunda a sexta, é o fim de
//     semana seguinte;
//   - mes: do dia 1 de manhã até o início da noite do último dia útil (segunda a sexta, sem contar
//     feriados); depois disso, já é o mês seguinte.
//
// Na precisão hora, a janela é o instante.
func (t Turnos) janela(p api.PrecisaoLembrete, quando time.Time) (inicio, fim time.Time) {
	dia := time.Date(quando.Year(), quando.Month(), quando.Day(), 0, 0, 0, 0, quando.Location())
	em := func(d time.Time, minutos int) time.Time { return d.Add(time.Duration(minutos) * time.Minute) }
	amanha := dia.AddDate(0, 0, 1)
	switch p {
	case api.PrecisaoDia:
		return em(dia, t.Manha), amanha
	case api.PrecisaoSemana:
		segunda := dia.AddDate(0, 0, -(int(dia.Weekday())+6)%7)
		if fim := em(segunda.AddDate(0, 0, 4), t.Noite); !quando.Before(fim) {
			segunda = segunda.AddDate(0, 0, 7)
		}
		return em(segunda, t.Manha), em(segunda.AddDate(0, 0, 4), t.Noite)
	case api.PrecisaoFimDeSemana:
		sabado := dia.AddDate(0, 0, (int(time.Saturday)-int(dia.Weekday())+7)%7)
		if dia.Weekday() == time.Sunday {
			sabado = dia.AddDate(0, 0, -1)
		}
		return em(sabado, t.Manha), sabado.AddDate(0, 0, 2)
	case api.PrecisaoMes:
		primeiro := dia.AddDate(0, 0, 1-dia.Day())
		if fim := em(ultimoDiaUtil(primeiro), t.Noite); !quando.Before(fim) {
			primeiro = primeiro.AddDate(0, 1, 0)
		}
		return em(primeiro, t.Manha), em(ultimoDiaUtil(primeiro), t.Noite)
	case api.PrecisaoTurno:
		minutos := quando.Hour()*60 + quando.Minute()
		switch {
		case minutos < t.Almoco:
			return em(dia, t.Manha), em(dia, t.Almoco)
		case minutos < t.Tarde:
			return em(dia, t.Almoco), em(dia, t.Tarde)
		case minutos < t.Noite:
			return em(dia, t.Tarde), em(dia, t.Noite)
		default:
			return em(dia, t.Noite), amanha
		}
	}
	return quando, quando
}

// ultimoDiaUtil: a meia-noite do último dia de segunda a sexta do mês de primeiro (o dia 1).
func ultimoDiaUtil(primeiro time.Time) time.Time {
	d := primeiro.AddDate(0, 1, -1)
	for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		d = d.AddDate(0, 0, -1)
	}
	return d
}

// inicio devolve onde começa o turno nomeado (manha, almoco, tarde ou noite) no dia de dia; outro nome,
// o começo do dia (a manhã).
func (t Turnos) inicio(dia time.Time, turno string) time.Time {
	minutos := t.Manha
	switch turno {
	case "almoco":
		minutos = t.Almoco
	case "tarde":
		minutos = t.Tarde
	case "noite":
		minutos = t.Noite
	}
	return time.Date(dia.Year(), dia.Month(), dia.Day(), 0, 0, 0, 0, dia.Location()).Add(time.Duration(minutos) * time.Minute)
}

// marcar põe quando no início da janela da precisão e recusa a janela que já acabou.
func (s *Servidor) marcar(p api.PrecisaoLembrete, quando time.Time) (time.Time, error) {
	if !precisaoValida(p) {
		return quando, falha(http.StatusBadRequest, "precisão de lembrete inválida: %q (hora, turno, dia, semana, fim_de_semana ou mes)", p)
	}
	inicio, fim := s.turnos().janela(p, quando)
	if p == api.PrecisaoHora {
		if quando.Before(s.agora()) {
			return quando, falha(http.StatusBadRequest, "a hora marcada já passou: %s", quando.Format(time.RFC3339))
		}
		return quando, nil
	}
	if !fim.After(s.agora()) {
		return quando, falha(http.StatusBadRequest, "o %s de %s já passou", p, quando.Format(time.RFC3339))
	}
	return inicio, nil
}
