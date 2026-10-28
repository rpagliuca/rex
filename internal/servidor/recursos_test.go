package servidor

import (
	"context"
	"strings"
	"testing"

	"github.com/rpagliuca/rex/api"
	"github.com/rpagliuca/rex/internal/recursos"
)

// medidorFalso devolve a medição que o teste quiser.
type medidorFalso struct{ m recursos.Medicao }

func (f *medidorFalso) Medir(context.Context) (recursos.Medicao, error) { return f.m, nil }
func (f *medidorFalso) Config() recursos.Config {
	return recursos.Config{MemoriaMinimaMB: 2500, SessaoNavegador: "agente-navegador"}
}

func TestRecursosDaMaquina(t *testing.T) {
	a := novoAmbiente(t)
	a.chamar("GET", "/recursos", "", nil, 404, nil)

	tarefa := a.tarefa("Medir")
	chat := "chat-do-agente"
	var criada api.SessaoCriada
	a.chamar("POST", "/sessoes", "", api.NovaSessao{Agente: "Claude Opus 5.5 Medium", TipoAgente: api.Ia,
		Servidor: "maquina-local", Chat: &chat, Tarefas: []string{tarefa.Id}}, 201, &criada)
	a.chamar("POST", "/sessoes/"+criada.Sessao.Id+"/espera", criada.Token, api.Espera{Mensagem: "aprovar o PR"}, 200, nil)

	a.servidor.ComMedidor(&medidorFalso{recursos.Medicao{
		TotalMB: 16000, DisponivelMB: 7000, MemoriaMinimaMB: 2500, Nucleos: 16,
		Sessoes: []recursos.Sessao{
			{Chat: chat, Nome: "medir", MB: 900},
			{Chat: "outro", Nome: "agente-navegador", MB: 800},
		},
		Navegador:     recursos.Grupo{Aberto: true, MB: 2000},
		Containers:    []recursos.Container{{Nome: "s1-sim", Projeto: "homolog", MB: 100}, {Nome: "s2-sim", Projeto: "homolog", MB: 150}},
		MaioresPorCPU: []recursos.Processo{{Pid: 7, Nome: "ffmpeg", CPU: 12.34}},
	}})
	var r api.Recursos
	a.chamar("GET", "/recursos", "", nil, 200, &r)
	if r.AbaixoDoMinimo || len(r.Sessoes) != 2 || r.Sessoes[0].Sessao == nil || *r.Sessoes[0].Sessao != criada.Sessao.Id ||
		r.Sessoes[0].Aguardando == nil || r.AgenteNavegador.MemoriaMb != 2800 || !r.AgenteNavegador.Aberto ||
		len(r.Docker) != 1 || r.Docker[0].MemoriaMb != 250 || r.MaioresPorCpu[0].Cpu != 12.3 {
		t.Fatalf("medição inesperada: %+v", r)
	}
	for _, trecho := range []string{"6,8 GB de 15,6 GB", "rodando, com o Chromium, 2,7 GB", "Steam: fechada", "homolog 0,2 GB",
		"Esperando o Rafael, da mais antiga: medir (" + criada.Sessao.Id, "ffmpeg"} {
		if !strings.Contains(r.Resumo, trecho) {
			t.Fatalf("resumo sem %q:\n%s", trecho, r.Resumo)
		}
	}
}

func TestLancamentoRecusadoComPoucaMemoria(t *testing.T) {
	a := novoAmbiente(t)
	l := &lancadorFalso{}
	a.servidor.ComLancador(l)
	a.servidor.AoMudarStatus(func(m MudancaDeStatus) { l.mudancas = append(l.mudancas, m) })
	medidor := &medidorFalso{recursos.Medicao{TotalMB: 16000, DisponivelMB: 1800, MemoriaMinimaMB: 2500}}
	a.servidor.ComMedidor(medidor)

	tarefa := a.tarefa("Pouca memória")
	var e api.Erro
	a.chamar("POST", "/tarefas/"+tarefa.Id+"/lancamento", "", api.PedidoDeLancamento{}, 409, &e)
	if e.Recursos == nil || !e.Recursos.AbaixoDoMinimo || !strings.Contains(e.Mensagem, "forcar") {
		t.Fatalf("recusa inesperada: %+v", e)
	}
	// Pôr em andamento pelo frontend também subiria um agente: mesma recusa.
	emAndamento := api.EmAndamento
	a.chamar("PATCH", "/tarefas/"+tarefa.Id, "", api.AlteracaoTarefa{Status: &emAndamento}, 409, nil)
	var t1 api.Tarefa
	a.chamar("GET", "/tarefas/"+tarefa.Id, "", nil, 200, &t1)
	if t1.Status != api.AFazer {
		t.Fatalf("a recusa mudou o status: %s", t1.Status)
	}

	forcar := true
	a.chamar("POST", "/tarefas/"+tarefa.Id+"/lancamento", "", api.PedidoDeLancamento{Forcar: &forcar}, 200, nil)

	// Uma sessão que põe a tarefa em andamento trabalha nela ela mesma: nada sobe, nada a medir.
	outra := a.tarefa("Da própria sessão")
	s := a.sessao("Claude Opus 5.5 Medium", outra.Id)
	a.chamar("PATCH", "/tarefas/"+outra.Id, s.Token, api.AlteracaoTarefa{Status: &emAndamento}, 200, nil)

	medidor.m.DisponivelMB = 6000
	terceira := a.tarefa("Com memória")
	a.chamar("POST", "/tarefas/"+terceira.Id+"/lancamento", "", api.PedidoDeLancamento{}, 200, nil)
}
