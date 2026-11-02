package servidor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rpagliuca/rex/api"
)

// lancadorDaOrfa decide a conferência da orfandade.
type lancadorDaOrfa struct {
	lancadorFalso
	vivo error
}

func (l *lancadorDaOrfa) ConferirOrfa(api.Sessao) error { return l.vivo }

func depoisDaEspera(a *ambiente) {
	a.servidor.agora = func() time.Time { return time.Now().UTC().Add(esperaDaOrfa + time.Minute) }
}

func TestEncerrarSessaoOrfa(t *testing.T) {
	a := novoAmbiente(t)
	l := &lancadorDaOrfa{vivo: errors.New("o agente dela está no tmux deploy")}
	a.servidor.ComLancador(l)
	a.servidor.ComVoz(&vozFalsa{})

	tarefa, outra := a.tarefa("Deploy"), a.tarefa("Backup")
	anterior := a.sessao("Claude Opus 5.5 Medium", tarefa.Id)
	a.chamar("POST", "/sessoes/"+anterior.Sessao.Id+"/encerramento", anterior.Token, api.Encerramento{Resumo: "falta o passo 3"}, 200, nil)

	journal := "laptop:/j/journal.md"
	s := a.sessao("Claude Opus 5.5 Medium", tarefa.Id, outra.Id)
	a.chamar("PATCH", "/sessoes/"+s.Sessao.Id, s.Token, api.AlteracaoSessao{Journal: &journal}, 200, nil)
	depoisDaEspera(a)

	// Agente vivo: a recusa diz por quê, e nada muda.
	var recusa struct{ Mensagem string }
	caminho := "/sessoes/" + s.Sessao.Id + "/encerramento-de-orfa"
	a.chamar("POST", caminho, "", api.EncerramentoDeOrfa{}, 409, &recusa)
	if !strings.Contains(recusa.Mensagem, "tmux deploy") {
		t.Fatalf("recusa: %+v", recusa)
	}
	var x api.Sessao
	a.chamar("GET", "/sessoes/"+s.Sessao.Id, "", nil, 200, &x)
	if x.Status != api.Ativa {
		t.Fatalf("sessão viva encerrada: %+v", x)
	}

	// Órfã, pelo id da tarefa e com autor: libera as tarefas e guarda o resumo anterior.
	l.vivo = nil
	coordenador := a.sessao("Claude Opus 5.5 Medium")
	observacao := "parou no deploy de homologação"
	a.chamar("POST", "/sessoes/"+strings.ToLower(tarefa.Id)+"/encerramento-de-orfa", coordenador.Token,
		api.EncerramentoDeOrfa{Observacao: &observacao}, 200, &x)
	if x.Status != api.Encerrada || x.Resumo == nil || !strings.Contains(*x.Resumo, journal) || !strings.Contains(*x.Resumo, observacao) {
		t.Fatalf("encerrada: %+v", x)
	}
	var depois, semResumo api.Tarefa
	a.chamar("GET", "/tarefas/"+tarefa.Id, "", nil, 200, &depois)
	if depois.SessaoAtiva != nil || !strings.HasPrefix(depois.Resumo, "A sessão "+s.Sessao.Id) || !strings.HasSuffix(depois.Resumo, "falta o passo 3") {
		t.Fatalf("tarefa: %+v", depois)
	}
	a.chamar("GET", "/tarefas/"+outra.Id, "", nil, 200, &semResumo)
	if semResumo.SessaoAtiva != nil || strings.Contains(semResumo.Resumo, "anterior") {
		t.Fatalf("tarefa sem resumo anterior: %+v", semResumo)
	}
	var eventos []api.Evento
	a.chamar("GET", "/eventos?entidade="+s.Sessao.Id+"&tipo=sessao_encerrada", "", nil, 200, &eventos)
	if len(eventos) != 1 || eventos[0].Autor != coordenador.Sessao.Id || eventos[0].Dados["orfa"] != true {
		t.Fatalf("eventos: %+v", eventos)
	}

	// A tarefa volta a aceitar sessão, e a órfã não encerra duas vezes.
	a.sessao("Claude Opus 5.5 Medium", tarefa.Id)
	a.chamar("POST", caminho, "", api.EncerramentoDeOrfa{}, 409, nil)
}

func TestSessaoOrfaRecusaAtividadeRecente(t *testing.T) {
	a := novoAmbiente(t)
	a.chamar("POST", "/sessoes/S-0099/encerramento-de-orfa", "", api.EncerramentoDeOrfa{}, 404, nil)
	s := a.sessao("Claude Opus 5.5 Medium", a.tarefa("Deploy").Id)
	caminho := "/sessoes/" + s.Sessao.Id + "/encerramento-de-orfa"

	// Sem lançador não há como conferir.
	depoisDaEspera(a)
	a.chamar("POST", caminho, "", api.EncerramentoDeOrfa{}, 409, nil)

	// Sessão recém-aberta: o agente pode estar subindo.
	a.servidor.ComLancador(&lancadorDaOrfa{})
	a.servidor.agora = func() time.Time { return time.Now().UTC() }
	a.chamar("POST", caminho, "", api.EncerramentoDeOrfa{}, 409, nil)

	// Conversa mudando agora, com a sessão já antiga.
	conversa := filepath.Join(t.TempDir(), "c.jsonl")
	if err := os.WriteFile(conversa, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	a.servidor.ComVoz(&vozFalsa{conversa: conversa})
	agora := time.Now().UTC().Add(esperaDaOrfa + time.Minute)
	a.servidor.agora = func() time.Time { return agora }
	if err := os.Chtimes(conversa, agora, agora); err != nil {
		t.Fatal(err)
	}
	a.chamar("POST", caminho, "", api.EncerramentoDeOrfa{}, 409, nil)

	// Quieta há mais que a espera: pode.
	if err := os.Chtimes(conversa, agora.Add(-2*esperaDaOrfa), agora.Add(-2*esperaDaOrfa)); err != nil {
		t.Fatal(err)
	}
	a.chamar("POST", caminho, "", api.EncerramentoDeOrfa{}, 200, nil)
}
