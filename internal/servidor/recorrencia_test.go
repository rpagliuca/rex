package servidor

import (
	"testing"
	"time"

	"github.com/rpagliuca/rex/api"
)

func TestAvancar(t *testing.T) {
	fuso := time.FixedZone("-03", -3*3600)
	casos := []struct {
		ancora time.Time
		r      api.RecorrenciaLembrete
		n      int
		quer   string
	}{
		{time.Date(2026, 12, 1, 8, 0, 0, 0, fuso), api.RecorrenciaMensal, 1, "2027-01-01T08:00:00-03:00"},
		{time.Date(2026, 1, 31, 9, 30, 0, 0, fuso), api.RecorrenciaMensal, 1, "2026-02-28T09:30:00-03:00"},
		{time.Date(2026, 1, 31, 9, 30, 0, 0, fuso), api.RecorrenciaMensal, 2, "2026-03-31T09:30:00-03:00"},
		{time.Date(2028, 2, 29, 8, 0, 0, 0, fuso), api.RecorrenciaAnual, 1, "2029-02-28T08:00:00-03:00"},
		{time.Date(2026, 11, 4, 8, 0, 0, 0, fuso), api.RecorrenciaSemanal, 2, "2026-11-18T08:00:00-03:00"},
		{time.Date(2026, 11, 4, 8, 0, 0, 0, fuso), api.RecorrenciaDiaria, 3, "2026-11-07T08:00:00-03:00"},
	}
	for _, c := range casos {
		if got := avancar(c.ancora, c.r, c.n).Format(time.RFC3339); got != c.quer {
			t.Errorf("avancar(%s, %s, %d) = %s, quer %s", c.ancora.Format(time.RFC3339), c.r, c.n, got, c.quer)
		}
	}
}

func TestLembreteRecorrente(t *testing.T) {
	a := novoAmbiente(t)
	fuso := time.FixedZone("-03", -3*3600)
	agora := a.relogio(time.Date(2026, 11, 4, 12, 0, 0, 0, fuso))
	mensal := api.RecorrenciaMensal
	dia := api.PrecisaoDia

	// Precisão mais larga que a recorrência é recusada; intervalo sem recorrência também.
	semana := api.PrecisaoSemana
	diaria := api.RecorrenciaDiaria
	a.chamar("POST", "/lembretes", "", api.NovoLembrete{Texto: "x", Quando: agora.AddDate(0, 0, 7), Precisao: &semana, Recorrencia: &diaria, Organizacao: "Exemplo"}, 400, nil)
	a.chamar("POST", "/lembretes", "", api.NovoLembrete{Texto: "x", Quando: agora.AddDate(0, 0, 7), Precisao: &dia, Intervalo: de(2), Organizacao: "Exemplo"}, 400, nil)

	var l api.Lembrete
	a.chamar("POST", "/lembretes", "", api.NovoLembrete{Texto: "pagar a fatura", Curto: ptr("fatura"), Quando: time.Date(2026, 12, 1, 15, 0, 0, 0, fuso),
		Precisao: &dia, Recorrencia: &mensal, Organizacao: "Exemplo"}, 201, &l)
	if l.Recorrencia == nil || *l.Recorrencia != mensal || *l.Intervalo != 1 || *l.Serie != l.Id || *l.Ocorrencia != 0 || l.Seguinte != nil ||
		l.Quando.Format(time.RFC3339) != "2026-12-01T08:00:00-03:00" {
		t.Fatalf("recorrente inesperado: %+v", l)
	}
	primeiro := l.Id

	// Disparar gera a próxima, com os mesmos dados; a listagem de recorrentes mostra a ponta da série.
	*agora = time.Date(2026, 12, 1, 8, 0, 5, 0, fuso)
	if n, _ := a.servidor.DispararVencidos(); n != 1 {
		t.Fatalf("esperava 1 disparo, veio %d", n)
	}
	a.chamar("GET", "/lembretes/"+primeiro, "", nil, 200, &l)
	if l.Status != api.Disparado || l.Seguinte == nil {
		t.Fatalf("esperava disparado com seguinte: %+v", l)
	}
	var s api.Lembrete
	a.chamar("GET", "/lembretes/"+*l.Seguinte, "", nil, 200, &s)
	if s.Status != api.Agendado || s.Quando.Format(time.RFC3339) != "2027-01-01T08:00:00-03:00" || *s.Serie != primeiro || *s.Ocorrencia != 1 ||
		*s.Curto != "fatura" || s.Texto != "pagar a fatura" || s.Precisao != dia {
		t.Fatalf("seguinte inesperado: %+v", s)
	}
	var recorrentes []api.Lembrete
	a.chamar("GET", "/lembretes?recorrentes=true", "", nil, 200, &recorrentes)
	if len(recorrentes) != 1 || recorrentes[0].Id != s.Id {
		t.Fatalf("recorrentes: %+v", recorrentes)
	}

	// Confirmar a disparada não gera outra; confirmar a próxima antes da hora gera a de fevereiro.
	a.chamar("POST", "/lembretes/"+primeiro+"/confirmacao", "", nil, 200, nil)
	a.chamar("POST", "/lembretes/"+s.Id+"/confirmacao", "", nil, 200, &s)
	if s.Seguinte == nil {
		t.Fatalf("confirmar antes da hora não gerou a seguinte: %+v", s)
	}
	var fev api.Lembrete
	a.chamar("GET", "/lembretes/"+*s.Seguinte, "", nil, 200, &fev)
	if fev.Quando.Format(time.RFC3339) != "2027-02-01T08:00:00-03:00" || *fev.Ocorrencia != 2 {
		t.Fatalf("fevereiro inesperado: %+v", fev)
	}

	// Adiar não desloca a série: adiada para dia 3, a seguinte continua em 1º de março.
	*agora = time.Date(2027, 2, 1, 9, 0, 0, 0, fuso)
	a.servidor.DispararVencidos()
	a.chamar("GET", "/lembretes/"+fev.Id, "", nil, 200, &fev)
	var mar api.Lembrete
	a.chamar("GET", "/lembretes/"+*fev.Seguinte, "", nil, 200, &mar)
	if mar.Quando.Format(time.RFC3339) != "2027-03-01T08:00:00-03:00" {
		t.Fatalf("março inesperado: %+v", mar)
	}

	// Servidor parado meses: a próxima pula as janelas que já acabaram.
	*agora = time.Date(2027, 6, 15, 12, 0, 0, 0, fuso)
	a.servidor.DispararVencidos()
	a.chamar("GET", "/lembretes/"+mar.Id, "", nil, 200, &mar)
	var jul api.Lembrete
	a.chamar("GET", "/lembretes/"+*mar.Seguinte, "", nil, 200, &jul)
	if jul.Quando.Format(time.RFC3339) != "2027-07-01T08:00:00-03:00" || *jul.Ocorrencia != 7 {
		t.Fatalf("julho inesperado: %+v", jul)
	}

	// Cancelar encerra a série: disparar depois não gera nada, e ela sai dos recorrentes.
	a.chamar("POST", "/lembretes/"+jul.Id+"/cancelamento", "", nil, 200, nil)
	a.chamar("GET", "/lembretes?recorrentes=true", "", nil, 200, &recorrentes)
	if len(recorrentes) != 0 {
		t.Fatalf("série cancelada continua: %+v", recorrentes)
	}
}

func TestTornarRecorrente(t *testing.T) {
	a := novoAmbiente(t)
	fuso := time.FixedZone("-03", -3*3600)
	agora := a.relogio(time.Date(2026, 11, 4, 12, 0, 0, 0, fuso))
	dia := api.PrecisaoDia
	var l api.Lembrete
	a.chamar("POST", "/lembretes", "", api.NovoLembrete{Texto: "aprovar", Quando: time.Date(2026, 12, 1, 8, 0, 0, 0, fuso), Precisao: &dia, Organizacao: "Exemplo"}, 201, &l)
	if l.Recorrencia != nil || l.Serie != nil {
		t.Fatalf("avulso com série: %+v", l)
	}

	// Avulso vira mensal, ancorado na própria hora; a precisão de semana com diária é recusada.
	a.chamar("PATCH", "/lembretes/"+l.Id, "", api.AlteracaoLembrete{Recorrencia: ptr("mensal")}, 200, &l)
	if l.Recorrencia == nil || *l.Recorrencia != api.RecorrenciaMensal || *l.Serie != l.Id || *l.Ocorrencia != 0 {
		t.Fatalf("não virou recorrente: %+v", l)
	}
	a.chamar("PATCH", "/lembretes/"+l.Id, "", api.AlteracaoLembrete{Recorrencia: ptr("diaria"), Precisao: de(api.PrecisaoSemana)}, 400, nil)
	a.chamar("PATCH", "/lembretes/"+l.Id, "", api.AlteracaoLembrete{Recorrencia: ptr("quinzenal")}, 400, nil)

	// A cada 2 meses: dispara em dezembro e a seguinte cai em fevereiro.
	a.chamar("PATCH", "/lembretes/"+l.Id, "", api.AlteracaoLembrete{Intervalo: de(2)}, 200, &l)
	*agora = time.Date(2026, 12, 1, 8, 0, 1, 0, fuso)
	a.servidor.DispararVencidos()
	a.chamar("GET", "/lembretes/"+l.Id, "", nil, 200, &l)
	var s api.Lembrete
	a.chamar("GET", "/lembretes/"+*l.Seguinte, "", nil, 200, &s)
	if s.Quando.Format(time.RFC3339) != "2027-02-01T08:00:00-03:00" || *s.Intervalo != 2 {
		t.Fatalf("seguinte a cada 2 meses: %+v", s)
	}

	// Vazio torna avulso: a seguinte, ao disparar, não gera outra.
	// (decodificar num valor novo: o JSON omite a recorrência que sumiu)
	id := s.Id
	s = api.Lembrete{}
	a.chamar("PATCH", "/lembretes/"+id, "", api.AlteracaoLembrete{Recorrencia: ptr("")}, 200, &s)
	if s.Recorrencia != nil || *s.Serie != l.Id {
		t.Fatalf("esperava avulso que guarda a série: %+v", s)
	}
	*agora = time.Date(2027, 2, 1, 8, 0, 1, 0, fuso)
	a.servidor.DispararVencidos()
	s = api.Lembrete{}
	a.chamar("GET", "/lembretes/"+id, "", nil, 200, &s)
	if s.Status != api.Disparado || s.Seguinte != nil {
		t.Fatalf("avulso gerou seguinte: %+v", s)
	}

	// Avulso já disparado que vira recorrente gera a próxima na hora.
	a.chamar("PATCH", "/lembretes/"+s.Id, "", api.AlteracaoLembrete{Recorrencia: ptr("semanal")}, 200, &s)
	if s.Seguinte == nil {
		t.Fatalf("disparado tornado recorrente sem seguinte: %+v", s)
	}
}

func de[T any](v T) *T { return &v }
