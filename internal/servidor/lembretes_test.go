package servidor

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/rpagliuca/rex/api"
)

// relogio fixa a hora do servidor; avancar a muda.
func (a *ambiente) relogio(t time.Time) *time.Time {
	agora := t
	a.servidor.agora = func() time.Time { return agora.UTC() }
	return &agora
}

func TestCicloDoLembrete(t *testing.T) {
	a := novoAmbiente(t)
	fuso := time.FixedZone("-03", -3*3600)
	agora := a.relogio(time.Date(2026, 10, 20, 14, 0, 0, 0, fuso))
	a.tarefa("Pagar contas")

	quando := time.Date(2026, 10, 20, 15, 0, 0, 0, fuso)
	a.chamar("POST", "/lembretes", "", api.NovoLembrete{Texto: "x", Quando: agora.Add(-time.Minute), Organizacao: "Exemplo"}, 400, nil)
	var l api.Lembrete
	a.chamar("POST", "/lembretes", "", api.NovoLembrete{Texto: "ligar para o contador", Quando: quando, Organizacao: "Exemplo", Tarefa: ptr("T-0001")}, 201, &l)
	if l.Id != "L-0001" || l.Status != api.Agendado || l.Autor != "local" || l.Quando.Format(time.RFC3339) != "2026-10-20T15:00:00-03:00" {
		t.Fatalf("lembrete inesperado: %+v", l)
	}

	// Antes da hora, nada dispara; depois, dispara uma vez.
	if n, _ := a.servidor.DispararVencidos(); n != 0 {
		t.Fatalf("disparou antes da hora: %d", n)
	}
	*agora = quando.Add(10 * time.Second)
	if n, _ := a.servidor.DispararVencidos(); n != 1 {
		t.Fatalf("esperava 1 disparo, veio %d", n)
	}
	if n, _ := a.servidor.DispararVencidos(); n != 0 {
		t.Fatalf("disparou de novo: %d", n)
	}
	a.chamar("GET", "/lembretes/L-0001", "", nil, 200, &l)
	if l.Status != api.Disparado || *l.Atrasado {
		t.Fatalf("esperava disparado no horário: %+v", l)
	}

	// Adiar reagenda; na nova hora, com o servidor parado por 10 min, dispara atrasado.
	minutos := 5
	a.chamar("POST", "/lembretes/L-0001/adiamento", "", api.Adiamento{Minutos: &minutos}, 200, &l)
	if l.Status != api.Adiado || l.Adiamentos != 1 || l.Quando.Format(time.RFC3339) != "2026-10-20T15:05:10-03:00" {
		t.Fatalf("adiamento inesperado: %+v", l)
	}
	*agora = quando.Add(15 * time.Minute)
	a.servidor.DispararVencidos()
	a.chamar("GET", "/lembretes/L-0001", "", nil, 200, &l)
	if l.Status != api.Disparado || !*l.Atrasado {
		t.Fatalf("esperava disparado atrasado: %+v", l)
	}

	// Confirmar encerra; repetir não muda nada; depois disso, adiar é conflito.
	a.chamar("POST", "/lembretes/L-0001/confirmacao", "", nil, 200, &l)
	a.chamar("POST", "/lembretes/L-0001/confirmacao", "", nil, 200, &l)
	if l.Status != api.Confirmado || l.ConfirmadoEm == nil {
		t.Fatalf("esperava confirmado: %+v", l)
	}
	a.chamar("POST", "/lembretes/L-0001/adiamento", "", api.Adiamento{Minutos: &minutos}, 409, nil)

	var eventos []api.Evento
	tipo := "lembrete_criado,lembrete_disparado,lembrete_adiado,lembrete_confirmado"
	a.chamar("GET", "/eventos?entidade=L-0001&tipo="+tipo, "", nil, 200, &eventos)
	var tipos []string
	for _, e := range eventos {
		tipos = append(tipos, e.Tipo)
	}
	if got := len(tipos); got != 5 {
		t.Fatalf("eventos do lembrete: %v", tipos)
	}

	// Listagem padrão: só os que pedem atenção.
	var lista []api.Lembrete
	a.chamar("GET", "/lembretes", "", nil, 200, &lista)
	if len(lista) != 0 {
		t.Fatalf("confirmado não pede atenção: %+v", lista)
	}
	a.chamar("GET", "/lembretes?status=confirmado,cancelado", "", nil, 200, &lista)
	if len(lista) != 1 {
		t.Fatalf("filtro por status: %+v", lista)
	}
}

func TestLembretesDeTurnoEDia(t *testing.T) {
	a := novoAmbiente(t)
	fuso := time.FixedZone("-03", -3*3600)
	agora := a.relogio(time.Date(2026, 10, 20, 10, 0, 0, 0, fuso))
	precisao := func(p api.PrecisaoLembrete) *api.PrecisaoLembrete { return &p }
	em := func(dia, hora, minuto int) time.Time { return time.Date(2026, 10, dia, hora, minuto, 0, 0, fuso) }
	criar := func(texto string, quando time.Time, p api.PrecisaoLembrete, status int) api.Lembrete {
		var l api.Lembrete
		a.chamar("POST", "/lembretes", "", api.NovoLembrete{Texto: texto, Quando: quando, Precisao: precisao(p), Organizacao: "Exemplo"}, status, &l)
		return l
	}

	// Sem precisão, hora (compatível com os clientes de antes).
	var l api.Lembrete
	a.chamar("POST", "/lembretes", "", api.NovoLembrete{Texto: "x", Quando: em(20, 11, 0), Organizacao: "Exemplo"}, 201, &l)
	if l.Precisao != api.PrecisaoHora || l.Fim != nil {
		t.Fatalf("padrão deveria ser hora, sem fim: %+v", l)
	}

	// Turno e dia vão para o início da janela, e o fim vem junto.
	tarde := criar("comprar pão", em(21, 15, 30), api.PrecisaoTurno, 201)
	if tarde.Quando.Format(time.RFC3339) != "2026-10-21T13:00:00-03:00" || tarde.Fim.Format(time.RFC3339) != "2026-10-21T18:00:00-03:00" {
		t.Fatalf("turno da tarde: %+v", tarde)
	}
	noite := criar("ler", em(21, 23, 0), api.PrecisaoTurno, 201)
	if noite.Quando.Format(time.RFC3339) != "2026-10-21T18:00:00-03:00" || noite.Fim.Format(time.RFC3339) != "2026-10-22T00:00:00-03:00" {
		t.Fatalf("turno da noite: %+v", noite)
	}
	almoco := criar("pagar a conta", em(22, 12, 30), api.PrecisaoTurno, 201)
	if almoco.Quando.Format(time.RFC3339) != "2026-10-22T12:00:00-03:00" || almoco.Fim.Format(time.RFC3339) != "2026-10-22T13:00:00-03:00" {
		t.Fatalf("turno do almoço: %+v", almoco)
	}
	manha := criar("ligar", em(22, 11, 0), api.PrecisaoTurno, 201)
	if manha.Fim.Format(time.RFC3339) != "2026-10-22T12:00:00-03:00" {
		t.Fatalf("a manhã acaba no almoço: %+v", manha)
	}
	madrugada := criar("regar", em(21, 6, 0), api.PrecisaoTurno, 201)
	if madrugada.Quando.Format(time.RFC3339) != "2026-10-21T08:00:00-03:00" {
		t.Fatalf("antes da manhã conta como manhã: %+v", madrugada)
	}
	semana := criar("recapturar", em(22, 15, 0), api.PrecisaoSemana, 201)
	if semana.Quando.Format(time.RFC3339) != "2026-10-19T08:00:00-03:00" || semana.Fim.Format(time.RFC3339) != "2026-10-26T00:00:00-03:00" {
		t.Fatalf("semana (segunda a domingo): %+v", semana)
	}
	domingo := criar("revisar", time.Date(2026, 11, 1, 10, 0, 0, 0, fuso), api.PrecisaoSemana, 201)
	if domingo.Quando.Format(time.RFC3339) != "2026-10-26T08:00:00-03:00" {
		t.Fatalf("domingo é da semana que começou na segunda: %+v", domingo)
	}
	mes := criar("revisar acessos", time.Date(2027, 4, 20, 9, 0, 0, 0, fuso), api.PrecisaoMes, 201)
	if mes.Quando.Format(time.RFC3339) != "2027-04-01T08:00:00-03:00" || mes.Fim.Format(time.RFC3339) != "2027-05-01T00:00:00-03:00" {
		t.Fatalf("mês: %+v", mes)
	}
	dia := criar("pagar boleto", em(21, 9, 0), api.PrecisaoDia, 201)
	if !dia.Quando.Equal(madrugada.Quando) || dia.Fim.Format(time.RFC3339) != "2026-10-22T00:00:00-03:00" {
		t.Fatalf("dia: %+v", dia)
	}

	// Hoje: a manhã ainda não acabou (aceita, já vencido); a janela que acabou é recusada.
	hoje := criar("ligar para a escola", em(20, 9, 0), api.PrecisaoTurno, 201)
	if hoje.Quando.Format(time.RFC3339) != "2026-10-20T08:00:00-03:00" {
		t.Fatalf("manhã de hoje: %+v", hoje)
	}
	criar("x", em(19, 9, 0), api.PrecisaoDia, 400)
	a.chamar("POST", "/lembretes", "", api.NovoLembrete{Texto: "x", Quando: em(21, 9, 0), Precisao: precisao("ano"), Organizacao: "Exemplo"}, 400, nil)

	// Disparar depois do início da janela não é atraso.
	a.servidor.DispararVencidos()
	a.chamar("GET", "/lembretes/"+hoje.Id, "", nil, 200, &l)
	if l.Status != api.Disparado || *l.Atrasado {
		t.Fatalf("turno disparado não é atrasado: %+v", l)
	}

	// Os do mesmo início disparam juntos (o lote).
	*agora = em(21, 8, 0)
	if n, _ := a.servidor.DispararVencidos(); n != 3 {
		t.Fatalf("esperava o lote de 3 das 8h (o x das 11h de ontem também), veio %d", n)
	}

	// Mudar a precisão põe no início da nova janela; hora volta a ser exata.
	a.chamar("PATCH", "/lembretes/"+tarde.Id, "", api.AlteracaoLembrete{Precisao: precisao(api.PrecisaoDia)}, 200, &l)
	if l.Precisao != api.PrecisaoDia || l.Quando.Format(time.RFC3339) != "2026-10-21T08:00:00-03:00" {
		t.Fatalf("turno para dia: %+v", l)
	}
	quando := em(21, 16, 45)
	var exato api.Lembrete
	a.chamar("PATCH", "/lembretes/"+tarde.Id, "", api.AlteracaoLembrete{Precisao: precisao(api.PrecisaoHora), Quando: &quando}, 200, &exato)
	if exato.Precisao != api.PrecisaoHora || !exato.Quando.Equal(quando) || exato.Fim != nil {
		t.Fatalf("dia para hora: %+v", exato)
	}
}

func TestPendencias(t *testing.T) {
	a := novoAmbiente(t)
	agora := a.relogio(time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC))
	for i, minutos := range []int{30, 90, 300} {
		a.chamar("POST", "/lembretes", "", api.NovoLembrete{Texto: "lembrete " + string(rune('a'+i)), Quando: agora.Add(time.Duration(minutos) * time.Minute), Organizacao: "Exemplo"}, 201, nil)
	}
	*agora = agora.Add(31 * time.Minute)
	a.servidor.DispararVencidos()

	a.tarefa("Revisar")
	status := api.EmValidacao
	a.chamar("PATCH", "/tarefas/T-0001", "", api.AlteracaoTarefa{Status: &status}, 200, nil)
	s := a.sessao("Claude Opus 5.5 Medium")
	a.chamar("POST", "/sessoes/"+s.Sessao.Id+"/espera", s.Token, api.Espera{Mensagem: "aprovar o PR"}, 200, nil)
	a.chamar("POST", "/sessoes/"+s.Sessao.Id+"/espera", "", api.Espera{Mensagem: "x"}, 401, nil)

	var p api.Pendencias
	a.chamar("GET", "/integracao/pendencias", "", nil, 200, &p)
	if len(p.Lembretes) != 2 || p.Lembretes[0].Status != api.Disparado || p.Lembretes[1].Id != "L-0002" {
		t.Fatalf("lembretes pendentes: %+v", p.Lembretes)
	}
	if len(p.TarefasEmValidacao) != 1 || len(p.SessoesAguardando) != 1 || p.SessoesAguardando[0].Aguardando.Mensagem != "aprovar o PR" {
		t.Fatalf("pendências: %+v", p)
	}
	a.chamar("GET", "/integracao/pendencias?horizonte=600", "", nil, 200, &p)
	if len(p.Lembretes) != 3 {
		t.Fatalf("horizonte maior: %+v", p.Lembretes)
	}

	// O cursor continua o log a partir do retrato.
	cursor := p.UltimoEvento
	a.chamar("DELETE", "/sessoes/"+s.Sessao.Id+"/espera", s.Token, nil, 200, nil)
	var eventos []api.Evento
	a.chamar("GET", "/eventos?depois="+itoa(cursor), "", nil, 200, &eventos)
	if len(eventos) != 1 || eventos[0].Tipo != "sessao_retomada" {
		t.Fatalf("eventos depois do cursor: %+v", eventos)
	}
	a.chamar("GET", "/integracao/pendencias", "", nil, 200, &p)
	if len(p.SessoesAguardando) != 0 {
		t.Fatalf("sessão retomada ainda aguarda: %+v", p.SessoesAguardando)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

// capturar faz POST /captura e devolve o status, a frase e o id criado.
func (a *ambiente) capturar(token string, c api.Captura) (int, string, string) {
	a.t.Helper()
	corpo, _ := json.Marshal(c)
	req, _ := http.NewRequest("POST", a.srv.URL+"/captura", bytes.NewReader(corpo))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	frase, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(frase), resp.Header.Get("X-rex-Id")
}

func TestCaptura(t *testing.T) {
	a := novoAmbiente(t)
	fuso, _ := time.LoadLocation("America/Sao_Paulo")
	agora := a.relogio(time.Date(2026, 10, 20, 14, 10, 0, 0, fuso))
	lembrete := api.Captura{Tipo: api.TipoDeCapturaLembrete, Texto: "ligar para o contador amanhã às 9"}

	if st, _, _ := a.capturar("segredo", lembrete); st != 404 {
		t.Fatalf("captura desligada deveria ser 404: %d", st)
	}
	a.servidor.ComIntegracao(Integracao{Fuso: fuso, TokenDeCaptura: "segredo", OrganizacaoDaCaptura: "Pessoal"})
	if st, _, _ := a.capturar("errado", lembrete); st != 401 {
		t.Fatalf("token errado deveria ser 401: %d", st)
	}

	chave := "msg-1"
	lembrete.Chave = &chave
	st, frase, id := a.capturar("segredo", lembrete)
	if st != 201 || id != "L-0001" || frase != "Lembrete L-0001: ligar para o contador, quarta 21/10 às 09:00." {
		t.Fatalf("captura de lembrete: %d %q %q", st, frase, id)
	}
	// A fila reentrega: mesma chave, mesmo resultado, nada novo.
	if st2, frase2, id2 := a.capturar("segredo", lembrete); st2 != 200 || frase2 != frase || id2 != id {
		t.Fatalf("repetição: %d %q %q", st2, frase2, id2)
	}
	var l api.Lembrete
	a.chamar("GET", "/lembretes/L-0001", "", nil, 200, &l)
	if l.Autor != "captura" || l.Organizacao != "Pessoal" {
		t.Fatalf("lembrete capturado: %+v", l)
	}
	a.chamar("GET", "/lembretes/L-0002", "", nil, 404, nil)

	// Hora ambígua: pede de novo.
	if st, frase, _ := a.capturar("segredo", api.Captura{Tipo: api.TipoDeCapturaLembrete, Texto: "ligar às 3"}); st != 422 || frase == "" {
		t.Fatalf("ambígua: %d %q", st, frase)
	}

	// Ditado às 14:10, chegou às 14:40 pela fila: "daqui a 20 minutos" já passou; cria e dispara.
	dita := *agora
	*agora = agora.Add(30 * time.Minute)
	st, frase, id = a.capturar("segredo", api.Captura{Tipo: api.TipoDeCapturaLembrete, Texto: "tirar o bolo do forno daqui a 20 minutos", DitaEm: &dita})
	a.chamar("GET", "/lembretes/"+id, "", nil, 200, &l)
	if st != 201 || l.Status != api.Disparado || !*l.Atrasado || frase != "Lembrete L-0002: tirar o bolo do forno, terça 20/10 às 14:30. Chegou depois da hora e já está tocando." {
		t.Fatalf("atrasado: %d %q %+v", st, frase, l)
	}

	// Sem hora: turno ou dia, no início da janela da instalação; "hoje à tarde" às 14:40 ainda vale.
	st, frase, id = a.capturar("segredo", api.Captura{Tipo: api.TipoDeCapturaLembrete, Texto: "comprar pão hoje à tarde"})
	var turno api.Lembrete
	a.chamar("GET", "/lembretes/"+id, "", nil, 200, &turno)
	if st != 201 || turno.Precisao != api.PrecisaoTurno || turno.Quando.Format(time.RFC3339) != "2026-10-20T13:00:00-03:00" ||
		frase != "Lembrete L-0003: comprar pão, terça 20/10 à tarde." {
		t.Fatalf("turno: %d %q %+v", st, frase, turno)
	}
	st, frase, id = a.capturar("segredo", api.Captura{Tipo: api.TipoDeCapturaLembrete, Texto: "pagar o boleto amanhã"})
	var dia api.Lembrete
	a.chamar("GET", "/lembretes/"+id, "", nil, 200, &dia)
	if st != 201 || dia.Precisao != api.PrecisaoDia || dia.Quando.Format(time.RFC3339) != "2026-10-21T08:00:00-03:00" ||
		frase != "Lembrete L-0004: pagar o boleto, quarta 21/10, o dia todo." {
		t.Fatalf("dia: %d %q %+v", st, frase, dia)
	}
	// Semana e mês: no início da manhã da segunda e do dia 1; "nesta semana" ainda vale na terça.
	for _, c := range []struct {
		dito, quando, frase string
		precisao            api.PrecisaoLembrete
	}{
		{"recapturar os retentivos semana que vem", "2026-10-26T08:00:00-03:00", "recapturar os retentivos, na semana de 26/10.", api.PrecisaoSemana},
		{"ligar para o banco nesta semana", "2026-10-19T08:00:00-03:00", "ligar para o banco, na semana de 19/10.", api.PrecisaoSemana},
		{"revisar os acessos em abril", "2027-04-01T08:00:00-03:00", "revisar os acessos, em abril de 2027.", api.PrecisaoMes},
	} {
		st, frase, id := a.capturar("segredo", api.Captura{Tipo: api.TipoDeCapturaLembrete, Texto: c.dito})
		var x api.Lembrete
		a.chamar("GET", "/lembretes/"+id, "", nil, 200, &x)
		if st != 201 || x.Precisao != c.precisao || x.Quando.Format(time.RFC3339) != c.quando || frase != "Lembrete "+id+": "+c.frase {
			t.Fatalf("%q: %d %q %+v", c.dito, st, frase, x)
		}
	}
	if st, frase, _ := a.capturar("segredo", api.Captura{Tipo: api.TipoDeCapturaLembrete, Texto: "regar hoje de manhã"}); st != 422 || frase != "terça 20/10 de manhã já passou. Diga de novo com o dia, o turno ou a hora." {
		t.Fatalf("manhã que já passou: %d %q", st, frase)
	}

	// Tarefa: sempre a fazer, sem lançar agente.
	st, frase, id = a.capturar("segredo", api.Captura{Tipo: api.TipoDeCapturaTarefa, Texto: "Trocar o filtro da caixa d'água"})
	var tarefa api.Tarefa
	a.chamar("GET", "/tarefas/"+id, "", nil, 200, &tarefa)
	if st != 201 || tarefa.Status != api.AFazer || tarefa.Organizacao != "Pessoal" || frase != "Tarefa T-0001 criada: Trocar o filtro da caixa d'água." {
		t.Fatalf("tarefa: %d %q %+v", st, frase, tarefa)
	}

	// Requisito: verbatim, sob o zero da organização, sem prioridade.
	var zero api.Requisito
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Tudo", Organizacao: ptr("Geral")}, 201, nil)
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Vida pessoal em ordem", Organizacao: ptr("Pessoal")}, 201, &zero)
	a.chamar("PUT", "/organizacoes/Pessoal", "", api.DefinicaoDeOrganizacao{Zero: zero.Id}, 200, nil)
	st, _, id = a.capturar("segredo", api.Captura{Tipo: api.TipoDeCapturaRequisito, Texto: "o portão abre pelo celular"})
	var req api.Requisito
	a.chamar("GET", "/requisitos/"+id, "", nil, 200, &req)
	if st != 201 || req.Titulo != "o portão abre pelo celular" || len(req.DerivaDe) != 1 || req.DerivaDe[0] != zero.Id || req.Prioridade != nil {
		t.Fatalf("requisito: %d %+v", st, req)
	}

	// Simular não cria nada.
	simular := true
	if st, frase, id := a.capturar("segredo", api.Captura{Tipo: api.TipoDeCapturaTarefa, Texto: "x", Simular: &simular}); st != 200 || id != "" || frase != "Entendi a tarefa: x." {
		t.Fatalf("simulação: %d %q %q", st, frase, id)
	}
	if st, _, _ := a.capturar("segredo", api.Captura{Tipo: "outro", Texto: "x"}); st != 400 {
		t.Fatalf("tipo desconhecido: %d", st)
	}
}

// TestCurtoDoLembrete: a palavra-chave da ilha do iPhone é opcional, tem até 10 caracteres
// (contados em runas) e some quando alterada para vazio.
func TestCurtoDoLembrete(t *testing.T) {
	a := novoAmbiente(t)
	fuso := time.FixedZone("-03", -3*3600)
	a.relogio(time.Date(2026, 10, 26, 14, 0, 0, 0, fuso))
	quando := time.Date(2026, 10, 26, 16, 50, 0, 0, fuso)

	a.chamar("POST", "/lembretes", "", api.NovoLembrete{Texto: "x", Curto: ptr("natação123"), Quando: quando, Organizacao: "Exemplo"}, 201, nil)
	a.chamar("POST", "/lembretes", "", api.NovoLembrete{Texto: "x", Curto: ptr("natação1234"), Quando: quando, Organizacao: "Exemplo"}, 400, nil)
	var l api.Lembrete
	a.chamar("POST", "/lembretes", "", api.NovoLembrete{Texto: "Sair para buscar a Sofia no ballet", Curto: ptr(" ballet "), Quando: quando, Organizacao: "Exemplo"}, 201, &l)
	if l.Curto == nil || *l.Curto != "ballet" {
		t.Fatalf("curto: %+v", l.Curto)
	}
	a.chamar("PATCH", "/lembretes/"+l.Id, "", api.AlteracaoLembrete{Curto: ptr("balé")}, 200, &l)
	if l.Curto == nil || *l.Curto != "balé" {
		t.Fatalf("curto alterado: %+v", l.Curto)
	}
	id := l.Id
	l = api.Lembrete{} // a resposta sem curto não zera o campo de um struct reaproveitado
	a.chamar("PATCH", "/lembretes/"+id, "", api.AlteracaoLembrete{Curto: ptr("")}, 200, &l)
	if l.Curto != nil {
		t.Fatalf("curto vazio deveria sumir: %q", *l.Curto)
	}
	l = api.Lembrete{}
	a.chamar("POST", "/lembretes", "", api.NovoLembrete{Texto: "sem palavra-chave", Quando: quando, Organizacao: "Exemplo"}, 201, &l)
	if l.Curto != nil {
		t.Fatalf("sem curto: %q", *l.Curto)
	}
}
