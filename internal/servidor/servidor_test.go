package servidor

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rpagliuca/rex/api"
	"github.com/rpagliuca/rex/internal/banco"
)

type ambiente struct {
	servidor *Servidor
	t        *testing.T
	db       *sql.DB
	srv      *httptest.Server
}

func novoAmbiente(t *testing.T) *ambiente {
	t.Helper()
	db, err := banco.Abrir(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	s := Novo(db)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(func() { srv.Close(); db.Close() })
	return &ambiente{t: t, db: db, srv: srv, servidor: s}
}

// chamar faz a requisição, confere o status e decodifica a resposta em destino (se não for nil).
func (a *ambiente) chamar(metodo, caminho, token string, corpo any, esperado int, destino any) {
	a.t.Helper()
	var leitor *bytes.Reader
	if corpo != nil {
		texto, _ := json.Marshal(corpo)
		leitor = bytes.NewReader(texto)
	} else {
		leitor = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(metodo, a.srv.URL+caminho, leitor)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != esperado {
		var e api.Erro
		json.NewDecoder(resp.Body).Decode(&e)
		a.t.Fatalf("%s %s: status %d, esperado %d (%s)", metodo, caminho, resp.StatusCode, esperado, e.Mensagem)
	}
	if destino != nil {
		if err := json.NewDecoder(resp.Body).Decode(destino); err != nil {
			a.t.Fatal(err)
		}
	}
}

func (a *ambiente) tarefa(titulo string) api.Tarefa {
	a.t.Helper()
	var t api.Tarefa
	a.chamar("POST", "/tarefas", "", api.NovaTarefa{Titulo: titulo, Organizacao: "Exemplo"}, 201, &t)
	return t
}

func (a *ambiente) sessao(agente string, tarefas ...string) api.SessaoCriada {
	a.t.Helper()
	var s api.SessaoCriada
	a.chamar("POST", "/sessoes", "", api.NovaSessao{
		Agente: agente, TipoAgente: api.Ia, Servidor: "maquina-local", Tarefas: tarefas,
	}, 201, &s)
	return s
}

func TestTarefaExigeTituloEOrganizacao(t *testing.T) {
	a := novoAmbiente(t)
	a.chamar("POST", "/tarefas", "", api.NovaTarefa{Titulo: "x"}, 400, nil)
	a.chamar("POST", "/tarefas", "", api.NovaTarefa{Organizacao: "Exemplo"}, 400, nil)

	tarefa := a.tarefa("Configurar backup")
	if tarefa.Id != "T-0001" || tarefa.Status != api.AFazer {
		t.Fatalf("tarefa inesperada: %+v", tarefa)
	}
	estado := api.EmAndamento
	var alterada api.Tarefa
	a.chamar("PATCH", "/tarefas/T-0001", "", api.AlteracaoTarefa{Status: &estado}, 200, &alterada)
	if alterada.Status != api.EmAndamento {
		t.Fatalf("status não alterado: %s", alterada.Status)
	}
	a.chamar("GET", "/tarefas/T-0099", "", nil, 404, nil)
}

func TestUmaSessaoAtivaPorTarefa(t *testing.T) {
	a := novoAmbiente(t)
	a.tarefa("Configurar backup")
	a.tarefa("Revisar firewall")

	primeira := a.sessao("Claude Opus 5.5 Medium", "T-0001")
	var tarefa api.Tarefa
	a.chamar("GET", "/tarefas/T-0001", "", nil, 200, &tarefa)
	if tarefa.SessaoAtiva == nil || *tarefa.SessaoAtiva != primeira.Sessao.Id {
		t.Fatalf("tarefa sem a sessão ativa: %+v", tarefa)
	}

	// Outra sessão na mesma tarefa é recusada, inclusive por vínculo posterior.
	a.chamar("POST", "/sessoes", "", api.NovaSessao{
		Agente: "Claude Fable 5.1 High", TipoAgente: api.Ia, Servidor: "maquina-local", Tarefas: []string{"T-0001"},
	}, 409, nil)
	segunda := a.sessao("Claude Fable 5.1 High", "T-0002")
	a.chamar("POST", "/sessoes/"+segunda.Sessao.Id+"/tarefas", segunda.Token, api.Vinculo{Tarefa: "T-0001"}, 409, nil)

	// A recusa não deixou sessão órfã para trás.
	var sessoes []api.Sessao
	a.chamar("GET", "/sessoes", "", nil, 200, &sessoes)
	if len(sessoes) != 2 {
		t.Fatalf("esperava 2 sessões, há %d", len(sessoes))
	}
}

func TestEncerramentoExigeTokenESincronizaResumo(t *testing.T) {
	a := novoAmbiente(t)
	a.tarefa("Configurar backup")
	a.tarefa("Revisar firewall")
	s := a.sessao("Claude Opus 5.5 Medium", "T-0001", "T-0002")
	outra := a.sessao("Claude Opus 5.5 Medium", a.tarefa("Outra").Id)

	caminho := "/sessoes/" + s.Sessao.Id + "/encerramento"
	a.chamar("POST", caminho, "", api.Encerramento{Resumo: "feito"}, 401, nil)
	a.chamar("POST", caminho, outra.Token, api.Encerramento{Resumo: "feito"}, 401, nil)
	a.chamar("POST", caminho, s.Token, api.Encerramento{Resumo: " "}, 400, nil)

	journal := "github.com/exemplo/journals@abc123:2026/s-0001.md"
	var encerrada api.Sessao
	a.chamar("POST", caminho, s.Token, api.Encerramento{Resumo: "Segundo destino configurado; falta testar a restauração.", Journal: &journal}, 200, &encerrada)
	if encerrada.Status != api.Encerrada || encerrada.EncerradaEm == nil || *encerrada.Journal != journal ||
		encerrada.Resumo == nil || *encerrada.Resumo != "Segundo destino configurado; falta testar a restauração." {
		t.Fatalf("sessão não encerrada corretamente: %+v", encerrada)
	}

	for _, id := range []string{"T-0001", "T-0002"} {
		var tarefa api.Tarefa
		a.chamar("GET", "/tarefas/"+id, "", nil, 200, &tarefa)
		if tarefa.Resumo != "Segundo destino configurado; falta testar a restauração." || tarefa.SessaoAtiva != nil {
			t.Fatalf("%s sem resumo ou ainda presa: %+v", id, tarefa)
		}
	}

	// Liberada, a tarefa aceita nova sessão; o token antigo não serve para mais nada.
	a.sessao("Claude Fable 5.1 High", "T-0001")
	a.chamar("POST", caminho, s.Token, api.Encerramento{Resumo: "de novo"}, 409, nil)
	a.chamar("POST", "/tarefas", s.Token, api.NovaTarefa{Titulo: "x", Organizacao: "Exemplo"}, 401, nil)

	var historico []api.Sessao
	a.chamar("GET", "/tarefas/T-0001/sessoes", "", nil, 200, &historico)
	if len(historico) != 2 || historico[1].Id != s.Sessao.Id {
		t.Fatalf("histórico inesperado: %+v", historico)
	}
}

func TestRepasseTrocaAgenteEmUmaOperacao(t *testing.T) {
	a := novoAmbiente(t)
	a.tarefa("Configurar backup")
	s := a.sessao("Claude Opus 5.5 Medium", "T-0001")
	caminho := "/sessoes/" + s.Sessao.Id + "/repasse"

	a.chamar("POST", caminho, s.Token, api.Repasse{Agente: "Claude Opus 5.5 Medium", TipoAgente: api.Ia}, 400, nil)

	var nova api.SessaoCriada
	a.chamar("POST", caminho, s.Token, api.Repasse{Agente: "Claude Fable 5.1 High", TipoAgente: api.Ia}, 201, &nova)
	if nova.Sessao.Anterior == nil || *nova.Sessao.Anterior != s.Sessao.Id ||
		len(nova.Sessao.Tarefas) != 1 || nova.Sessao.Tarefas[0] != "T-0001" || nova.Sessao.Servidor != "maquina-local" {
		t.Fatalf("sessão nova inesperada: %+v", nova.Sessao)
	}

	var antiga api.Sessao
	a.chamar("GET", "/sessoes/"+s.Sessao.Id, "", nil, 200, &antiga)
	if antiga.Status != api.Substituida || antiga.MotivoEncerramento == nil || *antiga.MotivoEncerramento != api.TrocaDeAgente {
		t.Fatalf("sessão antiga não marcada como substituída: %+v", antiga)
	}

	var tarefa api.Tarefa
	a.chamar("GET", "/tarefas/T-0001", "", nil, 200, &tarefa)
	if *tarefa.SessaoAtiva != nova.Sessao.Id {
		t.Fatalf("a tarefa não passou para a sessão nova: %+v", tarefa)
	}

	// A sessão antiga não pode repassar de novo.
	a.chamar("POST", caminho, s.Token, api.Repasse{Agente: "Rafael", TipoAgente: api.Humano}, 409, nil)
}

func TestInvariantesNoBanco(t *testing.T) {
	a := novoAmbiente(t)
	a.tarefa("Configurar backup")
	a.sessao("Claude Opus 5.5 Medium", "T-0001")

	// Mesmo escrevendo direto no banco, as invariantes valem.
	if _, err := a.db.Exec(`UPDATE sessoes SET agente = 'outro' WHERE id = 1`); err == nil {
		t.Fatal("o banco aceitou trocar o agente da sessão")
	}
	if _, err := a.db.Exec(`INSERT INTO sessoes (agente, tipo_agente, servidor, token_hash, iniciada_em)
		VALUES ('x', 'ia', 'y', 'h', 'agora')`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO sessao_tarefa (sessao_id, tarefa_id) VALUES (2, 1)`); err == nil {
		t.Fatal("o banco aceitou duas sessões ativas na mesma tarefa")
	}
	if _, err := a.db.Exec(`UPDATE eventos SET autor = 'outro'`); err == nil {
		t.Fatal("o banco aceitou alterar o log de eventos")
	}
	if _, err := a.db.Exec(`DELETE FROM eventos`); err == nil {
		t.Fatal("o banco aceitou apagar o log de eventos")
	}
}

func TestEventosRegistramAutor(t *testing.T) {
	a := novoAmbiente(t)
	a.tarefa("Configurar backup")
	s := a.sessao("Claude Opus 5.5 Medium", "T-0001")
	resumo := "andamento"
	a.chamar("PATCH", "/tarefas/T-0001", s.Token, api.AlteracaoTarefa{Resumo: &resumo}, 200, nil)

	var eventos []api.Evento
	a.chamar("GET", "/eventos?entidade=T-0001", "", nil, 200, &eventos)
	if len(eventos) != 2 {
		t.Fatalf("esperava 2 eventos da tarefa, há %d: %+v", len(eventos), eventos)
	}
	if eventos[0].Tipo != "tarefa_criada" || eventos[0].Autor != "local" {
		t.Fatalf("evento de criação inesperado: %+v", eventos[0])
	}
	if eventos[1].Tipo != "tarefa_alterada" || eventos[1].Autor != s.Sessao.Id {
		t.Fatalf("alteração não atribuída à sessão: %+v", eventos[1])
	}
}

func TestImportacaoPreservaDatasEHistorico(t *testing.T) {
	a := novoAmbiente(t)
	inicio := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	journalA, journalB := "maquina-a:/tarefas/x/CLAUDE.md", "maquina-b:/tarefas/x/CLAUDE.md"
	var tarefa api.Tarefa
	a.chamar("POST", "/importacao", "", api.ImportacaoTarefa{
		Titulo: "Tarefa antiga", Organizacao: "Exemplo", Status: api.Pausada, CriadaEm: inicio, Origem: "pasta x",
		Sessoes: []api.SessaoImportada{
			{Agente: "Claude (modelo não registrado)", Servidor: "maquina-b", Journal: &journalB,
				IniciadaEm: inicio, EncerradaEm: inicio.Add(72 * time.Hour), Resumo: "mais recente"},
			{Agente: "Claude (modelo não registrado)", Servidor: "maquina-a", Journal: &journalA,
				IniciadaEm: inicio, EncerradaEm: inicio.Add(24 * time.Hour), Resumo: "mais antigo"},
		},
	}, 201, &tarefa)
	if !tarefa.CriadaEm.Equal(inicio) || tarefa.Resumo != "mais recente" || tarefa.SessaoAtiva != nil || tarefa.Status != api.Pausada {
		t.Fatalf("tarefa importada inesperada: %+v", tarefa)
	}

	var historico []api.Sessao
	a.chamar("GET", "/tarefas/"+tarefa.Id+"/sessoes", "", nil, 200, &historico)
	if len(historico) != 2 || historico[0].Status != api.Encerrada ||
		*historico[0].Resumo != "mais recente" || *historico[1].Resumo != "mais antigo" {
		t.Fatalf("histórico inesperado: %+v", historico)
	}

	// A tarefa importada fica livre para uma sessão nova.
	a.sessao("Claude Opus 5.5 Medium", tarefa.Id)
}

// lancadorFalso aceita só "Modelo A" e guarda as mudanças que recebeu.
type lancadorFalso struct{ mudancas []MudancaDeStatus }

func (l *lancadorFalso) ConferirOrfa(api.Sessao) error { return nil }

func (l *lancadorFalso) Opcoes() api.OpcoesDeLancamento {
	return api.OpcoesDeLancamento{Modelos: []string{"Modelo A"}, Esforcos: []string{"high"}, ModeloPadrao: "Modelo A"}
}

func (l *lancadorFalso) Validar(p *api.PedidoDeLancamento) error {
	if p.Modelo != nil && *p.Modelo != "Modelo A" {
		return errors.New("modelo desconhecido")
	}
	return nil
}

func TestLancamentoDeAgente(t *testing.T) {
	a := novoAmbiente(t)
	a.chamar("GET", "/lancador", "", nil, 404, nil)

	l := &lancadorFalso{}
	a.servidor.ComLancador(l)
	a.servidor.AoMudarStatus(func(m MudancaDeStatus) { l.mudancas = append(l.mudancas, m) })
	var opcoes api.OpcoesDeLancamento
	a.chamar("GET", "/lancador", "", nil, 200, &opcoes)
	if opcoes.ModeloPadrao != "Modelo A" {
		t.Fatalf("opções inesperadas: %+v", opcoes)
	}

	tarefa := a.tarefa("Reabrir")
	l.mudancas = nil // a criação também avisa
	modeloRuim := "Modelo Z"
	a.chamar("POST", "/tarefas/"+tarefa.Id+"/lancamento", "", api.PedidoDeLancamento{Modelo: &modeloRuim}, 400, nil)

	instrucao := "Comece pelo teste que falhou."
	var lancada api.Tarefa
	a.chamar("POST", "/tarefas/"+tarefa.Id+"/lancamento", "", api.PedidoDeLancamento{Instrucao: &instrucao}, 200, &lancada)
	if lancada.Status != api.EmAndamento || len(l.mudancas) != 1 || l.mudancas[0].Lancamento == nil ||
		*l.mudancas[0].Lancamento.Instrucao != instrucao {
		t.Fatalf("lançamento não avisado como esperado: %+v %+v", lancada, l.mudancas)
	}

	// Já em andamento e sem sessão: pode relançar. Com sessão ativa, não.
	a.chamar("POST", "/tarefas/"+tarefa.Id+"/lancamento", "", api.PedidoDeLancamento{}, 200, nil)
	a.sessao("Claude Opus 5.5 Medium", tarefa.Id)
	a.chamar("POST", "/tarefas/"+tarefa.Id+"/lancamento", "", api.PedidoDeLancamento{}, 409, nil)
}

func TestCadeiaDeRequisitos(t *testing.T) {
	a := novoAmbiente(t)
	var diretora, lei api.Origem
	a.chamar("POST", "/origens", "", api.NovaOrigem{Nome: "Diretora de operações", Tipo: api.Pessoa}, 201, &diretora)
	a.chamar("POST", "/origens", "", api.NovaOrigem{Nome: "Lei de proteção de dados", Tipo: api.Documento}, 201, &lei)
	a.chamar("POST", "/origens", "", api.NovaOrigem{Nome: "Diretora de operações", Tipo: api.Pessoa}, 409, nil)

	org := "Exemplo"
	var cumprir, copia, backup api.Requisito
	// O zero (primeiro requisito) precisa de organização: não tem de quem herdar.
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Sem organização"}, 400, nil)
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Cumprir a lei de dados", Organizacao: &org, Origem: &diretora.Id}, 201, &cumprir)
	// Organização herdada do pai.
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Cópia fora do ambiente", Origem: &lei.Id,
		DerivaDe: &[]string{cumprir.Id}}, 201, &copia)
	if copia.Organizacao != org || len(copia.DerivaDe) != 1 || copia.DerivaDe[0] != cumprir.Id {
		t.Fatalf("derivado inesperado: %+v", copia)
	}
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Backup em dois destinos", DerivaDe: &[]string{copia.Id}}, 201, &backup)

	// Grafo: mais de um pai.
	a.chamar("POST", "/requisitos/"+backup.Id+"/derivacoes", "", api.Derivacao{Pai: cumprir.Id}, 200, &backup)
	if len(backup.DerivaDe) != 2 {
		t.Fatalf("esperava dois pais: %+v", backup.DerivaDe)
	}
	// Ciclos são recusados, inclusive indiretos.
	a.chamar("POST", "/requisitos/"+cumprir.Id+"/derivacoes", "", api.Derivacao{Pai: backup.Id}, 409, nil)
	a.chamar("POST", "/requisitos/"+cumprir.Id+"/derivacoes", "", api.Derivacao{Pai: cumprir.Id}, 400, nil)

	a.chamar("GET", "/requisitos/"+cumprir.Id, "", nil, 200, &cumprir)
	if len(cumprir.Derivados) != 2 {
		t.Fatalf("o topo da cadeia devia listar os dois derivados: %+v", cumprir.Derivados)
	}

	// Tarefa ↔ requisito.
	tarefa := a.tarefa("Configurar o segundo destino")
	var vinculada api.Tarefa
	a.chamar("POST", "/tarefas/"+tarefa.Id+"/requisitos", "", api.VinculoRequisito{Requisito: backup.Id}, 200, &vinculada)
	if vinculada.Requisitos == nil || len(*vinculada.Requisitos) != 1 {
		t.Fatalf("vínculo não aparece na tarefa: %+v", vinculada)
	}
	a.chamar("GET", "/requisitos/"+backup.Id, "", nil, 200, &backup)
	if len(backup.Tarefas) != 1 || backup.Tarefas[0] != tarefa.Id {
		t.Fatalf("vínculo não aparece no requisito: %+v", backup)
	}

	// Desfazer e filtrar; o último pai não pode ser desfeito.
	a.chamar("DELETE", "/requisitos/"+backup.Id+"/derivacoes/"+cumprir.Id, "", nil, 200, &backup)
	a.chamar("DELETE", "/requisitos/"+backup.Id+"/derivacoes/"+cumprir.Id, "", nil, 404, nil)
	a.chamar("DELETE", "/requisitos/"+backup.Id+"/derivacoes/"+copia.Id, "", nil, 409, nil)

	// Sem deriva_de, o novo deriva do zero (o primeiro requisito).
	var solto api.Requisito
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Outro ramo"}, 201, &solto)
	if len(solto.DerivaDe) != 1 || solto.DerivaDe[0] != cumprir.Id || solto.Organizacao != org {
		t.Fatalf("o novo devia derivar do zero: %+v", solto)
	}
	var daLei []api.Requisito
	a.chamar("GET", "/requisitos?origem="+lei.Id, "", nil, 200, &daLei)
	if len(daLei) != 1 || daLei[0].Id != copia.Id {
		t.Fatalf("filtro por origem: %+v", daLei)
	}
}

func TestLimiteDeFilhosEAtencao(t *testing.T) {
	a := novoAmbiente(t)
	org := "Exemplo"
	var zero api.Requisito
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Zero", Organizacao: &org}, 201, &zero)
	for i := 0; i <= MaxFilhos; i++ {
		a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: fmt.Sprintf("Filho %d", i)}, 201, nil)
	}
	// Seis filhos é aceito, mas o pai pede atenção.
	a.chamar("GET", "/requisitos/"+zero.Id, "", nil, 200, &zero)
	if len(zero.Atencao) == 0 || zero.Atencao[0].Motivo != api.MuitosFilhos {
		t.Fatalf("o zero com seis filhos devia pedir atenção: %+v", zero.Atencao)
	}

	var filhos []api.Requisito
	a.chamar("GET", "/requisitos", "", nil, 200, &filhos)
	motivos := map[api.MotivoDeAtencao]bool{}
	for _, m := range filhos[1].Atencao {
		motivos[m.Motivo] = true
	}
	if !motivos[api.SemOrigem] || !motivos[api.SemDescricao] || !motivos[api.SemTarefa] {
		t.Fatalf("atenção esperada no filho solto: %+v", filhos[1].Atencao)
	}
	tarefa := a.tarefa("Atende o filho")
	a.chamar("POST", "/tarefas/"+tarefa.Id+"/requisitos", "", api.VinculoRequisito{Requisito: filhos[1].Id}, 200, nil)
	var filho api.Requisito
	a.chamar("GET", "/requisitos/"+filhos[1].Id, "", nil, 200, &filho)
	for _, m := range filho.Atencao {
		if m.Motivo == api.SemTarefa {
			t.Fatalf("com tarefa, não devia pedir atenção por falta dela: %+v", filho.Atencao)
		}
	}
}

func TestEstadoDoRequisito(t *testing.T) {
	a := novoAmbiente(t)
	org := "Exemplo"
	var zero, folha api.Requisito
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Zero", Organizacao: &org}, 201, &zero)
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Folha"}, 201, &folha)
	estado := func(id string) api.EstadoDoRequisito {
		var r api.Requisito
		a.chamar("GET", "/requisitos/"+id, "", nil, 200, &r)
		return r.Estado
	}
	mudar := func(tarefa string, status api.StatusTarefa) {
		a.chamar("PATCH", "/tarefas/"+tarefa, "", api.AlteracaoTarefa{Status: &status}, 200, nil)
	}
	// Sem tarefa nem execução, nada indica atendimento; o zero, sem tarefa, é decidido pela folha.
	if e := estado(folha.Id); e != api.NaoAtendido {
		t.Fatalf("sem tarefa nem execução: %s", e)
	}
	a.chamar("GET", "/requisitos/"+zero.Id, "", nil, 200, &zero)
	if zero.Estado != api.NaoAtendido || zero.EstadoProprio != api.NaoAtendido || len(zero.LimitadoPor) != 1 {
		t.Fatalf("o zero devia seguir a folha: %+v", zero)
	}
	tarefa := a.tarefa("Atender a folha")
	a.chamar("POST", "/tarefas/"+tarefa.Id+"/requisitos", "", api.VinculoRequisito{Requisito: folha.Id}, 200, nil)
	if e := estado(folha.Id); e != api.NaoAtendido {
		t.Fatalf("com tarefa a fazer: %s", e)
	}
	// O zero é limitado pela folha, menos madura que ele.
	a.chamar("GET", "/requisitos/"+zero.Id, "", nil, 200, &zero)
	if zero.Estado != api.NaoAtendido || len(zero.LimitadoPor) != 1 || zero.LimitadoPor[0] != folha.Id {
		t.Fatalf("o zero devia herdar o estado da folha: %+v", zero)
	}
	mudar(tarefa.Id, api.Pausada)
	mudar(tarefa.Id, api.Concluida)
	if e := estado(folha.Id); e != api.NaoValidado {
		t.Fatalf("tarefa concluída não prova nada: %s", e)
	}

	// Execução no prazo valida; com o prazo vencido, expira.
	dias := 30
	var e api.Execucao
	a.chamar("POST", "/requisitos/"+folha.Id+"/execucoes", "", api.NovaExecucao{Evidencia: "conferido", ValidadeDias: &dias}, 201, &e)
	if e.ValidaAte == nil || e.Id != "E-0001" {
		t.Fatalf("execução: %+v", e)
	}
	if got := estado(folha.Id); got != api.Validado {
		t.Fatalf("com execução no prazo: %s", got)
	}
	antiga := time.Now().AddDate(0, 0, -40)
	a.chamar("POST", "/requisitos/"+folha.Id+"/execucoes", "", api.NovaExecucao{Evidencia: "antiga", RealizadaEm: &antiga, ValidadeDias: &dias}, 201, nil)
	if got := estado(folha.Id); got != api.Validado {
		t.Fatalf("vale a execução mais recente, não a última registrada: %s", got)
	}
	um := 1
	a.chamar("POST", "/requisitos/"+zero.Id+"/execucoes", "", api.NovaExecucao{Evidencia: "velha", RealizadaEm: &antiga, ValidadeDias: &um}, 201, nil)
	if got := estado(zero.Id); got != api.ValidacaoExpirada {
		t.Fatalf("com o prazo vencido (e a folha validada): %s", got)
	}
	a.chamar("POST", "/requisitos/"+zero.Id+"/execucoes", "", api.NovaExecucao{Evidencia: " "}, 400, nil)

	// Tarefa reaberta derruba a validação; em andamento, está em trabalho.
	mudar(tarefa.Id, api.EmValidacao)
	if got := estado(folha.Id); got != api.NaoAtendido {
		t.Fatalf("tarefa aberta vence a execução: %s", got)
	}
	mudar(tarefa.Id, api.EmAndamento)
	if got, doZero := estado(folha.Id), estado(zero.Id); got != api.EmTrabalho || doZero != api.EmTrabalho {
		t.Fatalf("tarefa em andamento: folha %s, zero %s", got, doZero)
	}
	// A tarefa menos madura manda: outra a fazer deixa a folha não atendida.
	outra := a.tarefa("Outra parte da folha")
	a.chamar("POST", "/tarefas/"+outra.Id+"/requisitos", "", api.VinculoRequisito{Requisito: folha.Id}, 200, nil)
	if got := estado(folha.Id); got != api.NaoAtendido {
		t.Fatalf("com uma tarefa a fazer: %s", got)
	}
	var lista []api.Execucao
	a.chamar("GET", "/requisitos/"+folha.Id+"/execucoes", "", nil, 200, &lista)
	if len(lista) != 2 || lista[0].Evidencia != "conferido" {
		t.Fatalf("execuções da mais recente para a mais antiga: %+v", lista)
	}
}

func TestTemplates(t *testing.T) {
	a := novoAmbiente(t)
	org := "Exemplo"
	var zero api.Requisito
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Zero", Organizacao: &org}, 201, &zero)
	sete := 7
	var revisao, sobDemanda api.Template
	a.chamar("POST", "/templates", "", api.NovoTemplate{Titulo: "Revisar a fila", Organizacao: org, Descricao: ptr("Passos"),
		PeriodicidadeDias: &sete, Requisitos: &[]string{zero.Id}}, 201, &revisao)
	a.chamar("POST", "/templates", "", api.NovoTemplate{Titulo: "Restaurar backup", Organizacao: org}, 201, &sobDemanda)
	if !revisao.Devido || revisao.Id != "TP-0001" || sobDemanda.Devido {
		t.Fatalf("periódico nunca instanciado está devido; sob demanda nunca: %+v %+v", revisao, sobDemanda)
	}

	var tarefa api.Tarefa
	a.chamar("POST", "/templates/"+revisao.Id+"/instancias", "", api.PedidoDeInstancia{}, 201, &tarefa)
	if tarefa.Template == nil || *tarefa.Template != revisao.Id || tarefa.Descricao != "Passos" || len(*tarefa.Requisitos) != 1 || tarefa.Status != api.AFazer {
		t.Fatalf("instância: %+v", tarefa)
	}
	a.chamar("GET", "/templates/"+revisao.Id, "", nil, 200, &revisao)
	if revisao.Devido || revisao.InstanciaAberta == nil || len(revisao.Instancias) != 1 {
		t.Fatalf("com instância recente e aberta, não está devido: %+v", revisao)
	}

	// Concluída a instância, só volta a ser devido depois da periodicidade.
	concluida := api.Concluida
	a.chamar("PATCH", "/tarefas/"+tarefa.Id, "", api.AlteracaoTarefa{Status: &concluida}, 200, nil)
	var comContexto api.Tarefa
	a.chamar("POST", "/templates/"+sobDemanda.Id+"/instancias", "", api.PedidoDeInstancia{Contexto: ptr("o backup de ontem")}, 201, &comContexto)
	if !strings.HasPrefix(comContexto.Descricao, "## Contexto desta instância\n\no backup de ontem") {
		t.Fatalf("contexto no topo da descrição: %q", comContexto.Descricao)
	}
	var depois, sob api.Template
	a.chamar("GET", "/templates/"+revisao.Id, "", nil, 200, &depois)
	if depois.Devido || depois.ProximaEm == nil || depois.InstanciaAberta != nil {
		t.Fatalf("instanciado hoje, a cada 7 dias: %+v", depois)
	}
	zeroDias := 0
	a.chamar("PATCH", "/templates/"+revisao.Id, "", api.AlteracaoTemplate{PeriodicidadeDias: &zeroDias}, 200, &sob)
	if sob.PeriodicidadeDias != nil || sob.Devido {
		t.Fatalf("0 torna sob demanda: %+v", sob)
	}
	a.chamar("POST", "/templates", "", api.NovoTemplate{Titulo: "x", Organizacao: org, Requisitos: &[]string{"R-0099"}}, 404, nil)
}
func ptr(s string) *string { return &s }

func TestZeroPorOrganizacao(t *testing.T) {
	a := novoAmbiente(t)
	geral, yes, pessoal := "Geral", "Yes", "Pessoal"
	var zero, zeroYes, zeroPessoal, fora api.Requisito
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Zero", Organizacao: &geral}, 201, &zero)
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Yes", Organizacao: &yes}, 201, &zeroYes)
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Pessoal", Organizacao: &pessoal}, 201, &zeroPessoal)
	a.chamar("PUT", "/organizacoes/Yes", "", api.DefinicaoDeOrganizacao{Zero: zeroYes.Id}, 200, nil)
	a.chamar("PUT", "/organizacoes/Yes", "", api.DefinicaoDeOrganizacao{Zero: zeroPessoal.Id}, 400, nil)
	// Requisito da Yes sob um da Pessoal: fora da cadeia da Yes.
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Fora", Organizacao: &yes, DerivaDe: &[]string{zeroPessoal.Id}}, 201, &fora)
	tem := func(r api.Requisito) bool {
		for _, m := range r.Atencao {
			if m.Motivo == api.OutraOrganizacao {
				return true
			}
		}
		return false
	}
	a.chamar("GET", "/requisitos/"+zeroYes.Id, "", nil, 200, &zeroYes)
	a.chamar("GET", "/requisitos/"+zeroPessoal.Id, "", nil, 200, &zeroPessoal)
	if !tem(fora) || tem(zeroYes) || !tem(zeroPessoal) {
		t.Fatalf("só o zero atribuído deriva de fora: fora %v, zero Yes %v, Pessoal sem zero %v", tem(fora), tem(zeroYes), tem(zeroPessoal))
	}
	var orgs []api.Organizacao
	a.chamar("GET", "/organizacoes", "", nil, 200, &orgs)
	if len(orgs) != 3 || orgs[2].Nome != "Yes" || orgs[2].Zero == nil || *orgs[2].Zero != zeroYes.Id {
		t.Fatalf("organizações: %+v", orgs)
	}
}

func TestPrioridade(t *testing.T) {
	a := novoAmbiente(t)
	org := "Exemplo"
	var r api.Requisito
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Zero", Organizacao: &org}, 201, &r)
	if r.Prioridade != nil {
		t.Fatalf("nasce não triado: %+v", r)
	}
	var alta api.Requisito
	a.chamar("PATCH", "/requisitos/"+r.Id, "", api.AlteracaoRequisito{Prioridade: ptr("alta")}, 200, &alta)
	if alta.Prioridade == nil || *alta.Prioridade != api.Alta {
		t.Fatalf("alta: %+v", alta)
	}
	a.chamar("PATCH", "/requisitos/"+r.Id, "", api.AlteracaoRequisito{Prioridade: ptr("urgente")}, 400, nil)
	var limpo api.Requisito
	a.chamar("PATCH", "/requisitos/"+r.Id, "", api.AlteracaoRequisito{Prioridade: ptr("")}, 200, &limpo)
	if limpo.Prioridade != nil {
		t.Fatalf("vazio volta a não triado: %+v", limpo)
	}
}

// Os pontos ficam na ligação; a fatia é entre irmãos e o peso no total vem de cima (R-0432).
func TestPontosDeImportancia(t *testing.T) {
	a := novoAmbiente(t)
	geral := "Geral"
	var zero, yes, cart, pes, ambos api.Requisito
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Tudo bem", Organizacao: &geral}, 201, &zero)
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Yes"}, 201, &yes)
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Cartório"}, 201, &cart)
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Pessoal"}, 201, &pes)
	a.chamar("GET", "/requisitos/"+yes.Id, "", nil, 200, &yes)
	if yes.Importancia[0].Pontos != nil || !perto(yes.Peso, 1.0/3) {
		t.Fatalf("nasce não triado, contando 1: %+v", yes)
	}
	pontos := func(r api.Requisito, pai string, p int) api.Requisito {
		var x api.Requisito
		a.chamar("PUT", "/requisitos/"+r.Id+"/derivacoes/"+pai, "", api.Pontos{Pontos: &p}, 200, &x)
		return x
	}
	pontos(yes, zero.Id, 6)
	pontos(cart, zero.Id, 3)
	pontos(pes, zero.Id, 1)
	a.chamar("GET", "/requisitos/"+yes.Id, "", nil, 200, &yes)
	if !perto(yes.Peso, 0.6) || !perto(yes.Importancia[0].Fatia, 0.6) || *yes.Importancia[0].Pontos != 6 {
		t.Fatalf("Yes 6 de 10: %+v", yes)
	}
	// Mudar um valor recalcula só os irmãos: Yes de 6 para 20 vai a 20/24.
	yes = pontos(yes, zero.Id, 20)
	if !perto(yes.Peso, 20.0/24) {
		t.Fatalf("Yes 20 de 24: %+v", yes)
	}
	// Dois pais: o peso soma os dois caminhos. Sob o Cartório ele é filho único (100%); sob o
	// Pessoal, derivado depois, ganha 1 ponto por padrão e também é filho único.
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Comum", DerivaDe: &[]string{cart.Id}}, 201, &ambos)
	a.chamar("POST", "/requisitos/"+ambos.Id+"/derivacoes", "", api.Derivacao{Pai: pes.Id}, 200, &ambos)
	if p := ambos.Importancia[1].Pontos; p == nil || *p != 1 {
		t.Fatalf("segundo pai, 1 ponto: %+v", ambos.Importancia)
	}
	if !perto(ambos.Peso, 4.0/24) {
		t.Fatalf("peso somado dos dois pais: %v", ambos.Peso)
	}
	a.chamar("PUT", "/requisitos/"+ambos.Id+"/derivacoes/"+cart.Id, "", api.Pontos{Pontos: new(int)}, 400, nil)
	a.chamar("PUT", "/requisitos/"+ambos.Id+"/derivacoes/"+zero.Id, "", api.Pontos{}, 404, nil)
	// A tarefa pesa a soma dos requisitos dela.
	tarefa := a.tarefa("Atender o comum")
	a.chamar("POST", "/tarefas/"+tarefa.Id+"/requisitos", "", api.VinculoRequisito{Requisito: ambos.Id}, 200, nil)
	var tarefas []api.Tarefa
	a.chamar("GET", "/tarefas", "", nil, 200, &tarefas)
	if tarefas[0].Peso == nil || !perto(*tarefas[0].Peso, 4.0/24) {
		t.Fatalf("peso da tarefa: %+v", tarefas[0])
	}
	var arvore api.ArvoreDeRequisitos
	a.chamar("GET", "/arvore/requisitos", "", nil, 200, &arvore)
	if !strings.Contains(arvore.Texto, yes.Id+" [20 pts · 83,3% · 83,3% do total] Yes") {
		t.Fatalf("árvore: %s", arvore.Texto)
	}
}

func perto(a float32, b float64) bool { return math.Abs(float64(a)-b) < 1e-4 }

func TestComponentes(t *testing.T) {
	a := novoAmbiente(t)
	org := "Exemplo"
	var r api.Requisito
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Zero", Organizacao: &org}, 201, &r)
	tarefa := a.tarefa("Mexer no módulo")
	var c api.Componente
	a.chamar("POST", "/componentes", "", api.NovoComponente{Nome: "Módulo X", Organizacoes: []string{org}}, 201, &c)
	a.chamar("POST", "/componentes", "", api.NovoComponente{Nome: "Módulo X", Organizacoes: []string{org}}, 409, nil)
	a.chamar("POST", "/requisitos/"+r.Id+"/componentes", "", api.VinculoComponente{Componente: c.Id}, 200, &r)
	a.chamar("POST", "/tarefas/"+tarefa.Id+"/componentes", "", api.VinculoComponente{Componente: c.Id}, 200, &tarefa)
	if len(r.Componentes) != 1 || tarefa.Componentes == nil || len(*tarefa.Componentes) != 1 {
		t.Fatalf("vínculos: %+v %+v", r.Componentes, tarefa.Componentes)
	}
	a.chamar("GET", "/componentes/"+c.Id, "", nil, 200, &c)
	if len(c.Requisitos) != 1 || len(c.Tarefas) != 1 {
		t.Fatalf("componente: %+v", c)
	}
	var sem api.Requisito
	a.chamar("DELETE", "/requisitos/"+r.Id+"/componentes/"+c.Id, "", nil, 200, &sem)
	if len(sem.Componentes) != 0 {
		t.Fatalf("desvinculado: %+v", sem.Componentes)
	}
	a.chamar("POST", "/requisitos/"+r.Id+"/componentes", "", api.VinculoComponente{Componente: "C-0099"}, 404, nil)

	// Várias organizações; nenhuma é recusado.
	var duas api.Componente
	a.chamar("PATCH", "/componentes/"+c.Id, "", api.AlteracaoComponente{Organizacoes: &[]string{"Outra", org}}, 200, &duas)
	if len(duas.Organizacoes) != 2 || duas.Organizacoes[0] != org {
		t.Fatalf("organizações: %+v", duas.Organizacoes)
	}
	a.chamar("PATCH", "/componentes/"+c.Id, "", api.AlteracaoComponente{Organizacoes: &[]string{" "}}, 400, nil)
	a.chamar("POST", "/componentes", "", api.NovoComponente{Nome: "Sem organização", Organizacoes: []string{}}, 400, nil)
}

func TestBuscaArvoreETrilhas(t *testing.T) {
	a := novoAmbiente(t)
	geral, org := "Geral", "Cartório"
	var zero, cart, backup, internet api.Requisito
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Tudo bem", Organizacao: &geral}, 201, &zero)
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "O cartório opera", Organizacao: &org}, 201, &cart)
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Os backups têm duas cópias", DerivaDe: &[]string{cart.Id},
		Descricao: ptr("restic para S3")}, 201, &backup)
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "A internet não cai", DerivaDe: &[]string{cart.Id},
		Descricao: ptr("o backup não satura o link")}, 201, &internet)
	a.chamar("PUT", "/organizacoes/Cart%C3%B3rio", "", api.DefinicaoDeOrganizacao{Zero: cart.Id}, 200, nil)

	var achados []api.ResumoDeRequisito
	a.chamar("GET", "/busca/requisitos?q=c%C3%B3pia+do+backup", "", nil, 200, &achados)
	if len(achados) != 2 || achados[0].Id != backup.Id {
		t.Fatalf("título pesa mais, sem acento e com prefixo: %+v", achados)
	}
	var arvore api.ArvoreDeRequisitos
	a.chamar("GET", "/arvore/requisitos?organizacao=Cart%C3%B3rio", "", nil, 200, &arvore)
	if arvore.Requisitos != 3 || !strings.HasPrefix(arvore.Texto, cart.Id+" O cartório opera\n  ") {
		t.Fatalf("árvore da organização: %q", arvore.Texto)
	}
	var r api.Requisito
	a.chamar("GET", "/requisitos/"+backup.Id, "", nil, 200, &r)
	if r.Trilhas == nil || len(*r.Trilhas) != 1 || (*r.Trilhas)[0] != zero.Id+" Tudo bem › "+cart.Id+" O cartório opera" {
		t.Fatalf("trilha: %+v", r.Trilhas)
	}

	var c api.Componente
	a.chamar("POST", "/componentes", "", api.NovoComponente{Nome: "Backup", Organizacoes: []string{org}}, 201, &c)
	var tarefa api.Tarefa
	a.chamar("POST", "/tarefas", "", api.NovaTarefa{Titulo: "Testar restauração", Organizacao: org,
		Requisitos: &[]string{backup.Id}, Componentes: &[]string{c.Id}}, 201, &tarefa)
	if len(*tarefa.Requisitos) != 1 || len(*tarefa.Componentes) != 1 {
		t.Fatalf("tarefa já ligada na criação: %+v", tarefa)
	}
}

func TestSessaoRenomeada(t *testing.T) {
	a := novoAmbiente(t)
	a.tarefa("Configurar backup")
	var avisadas []api.Sessao
	a.servidor.AoRenomearSessao(func(x api.Sessao) { avisadas = append(avisadas, x) })

	lancada, err := a.servidor.AbrirSessaoLancada("T-0001", "Claude Opus 5.5 Medium", "maquina-local", "chat-1", "configurar-backup")
	if err != nil || lancada.Sessao.Nome == nil || *lancada.Sessao.Nome != "configurar-backup" {
		t.Fatalf("sessão lançada sem nome: %+v %v", lancada.Sessao, err)
	}

	caminho := "/sessoes/" + lancada.Sessao.Id
	a.chamar("PATCH", caminho, lancada.Token, api.AlteracaoSessao{Nome: ptr("  —  ")}, 400, nil)
	a.chamar("PATCH", caminho, "", api.AlteracaoSessao{Nome: ptr("backup-fotos")}, 401, nil)
	var x api.Sessao
	a.chamar("PATCH", caminho, lancada.Token, api.AlteracaoSessao{Nome: ptr("Restic das Fotos")}, 200, &x)
	if x.Nome == nil || *x.Nome != "restic-das-fotos" {
		t.Fatalf("nome não normalizado: %+v", x.Nome)
	}
	if len(avisadas) != 1 || *avisadas[0].Nome != "restic-das-fotos" {
		t.Fatalf("renomeação não avisada: %+v", avisadas)
	}

	// Só o journal: sem aviso de renomeação.
	a.chamar("PATCH", caminho, lancada.Token, api.AlteracaoSessao{Journal: ptr("maquina:/j.md")}, 200, nil)
	if len(avisadas) != 1 {
		t.Fatalf("avisou renomeação sem nome novo: %d", len(avisadas))
	}
}

func TestConsultaDeTarefas(t *testing.T) {
	a := novoAmbiente(t)
	org := "Exemplo"
	var r api.Requisito
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Zero", Organizacao: &org}, 201, &r)
	var c api.Componente
	a.chamar("POST", "/componentes", "", api.NovoComponente{Nome: "Módulo X", Organizacoes: []string{org}}, 201, &c)
	descricao := strings.Repeat("longa ", 50)
	validar := a.tarefa("Revisar o contrato")
	a.chamar("PATCH", "/tarefas/"+validar.Id, "", api.AlteracaoTarefa{Descricao: &descricao}, 200, nil)
	bloquear := a.tarefa("Esperar o fornecedor")
	a.tarefa("Pagar contas")
	var outra api.Tarefa
	a.chamar("POST", "/tarefas", "", api.NovaTarefa{Titulo: "Outra frente", Organizacao: "Outra",
		Requisitos: &[]string{r.Id}, Componentes: &[]string{c.Id}}, 201, &outra)
	for id, status := range map[string]api.StatusTarefa{validar.Id: api.EmValidacao, bloquear.Id: api.Bloqueada, outra.Id: api.EmValidacao} {
		a.chamar("PATCH", "/tarefas/"+id, "", api.AlteracaoTarefa{Status: &status}, 200, nil)
	}
	s := a.sessao("Agente", validar.Id)
	a.chamar("POST", "/sessoes/"+s.Sessao.Id+"/espera", s.Token, api.Espera{Mensagem: "aprovar o PR"}, 200, nil)

	a.chamar("GET", "/busca/tarefas", "", nil, 400, nil)
	a.chamar("GET", "/busca/tarefas?status=terminada", "", nil, 400, nil)

	consultar := func(filtro string) api.ConsultaDeTarefas {
		t.Helper()
		var out api.ConsultaDeTarefas
		a.chamar("GET", "/busca/tarefas?"+filtro, "", nil, 200, &out)
		return out
	}
	ids := func(c api.ConsultaDeTarefas) []string {
		var ids []string
		for _, x := range c.Tarefas {
			ids = append(ids, x.Id)
		}
		slices.Sort(ids)
		return ids
	}
	emValidacao := consultar("status=em_validacao&organizacao=Exemplo")
	if len(emValidacao.Tarefas) != 1 || emValidacao.Total != 1 || emValidacao.Fora != 0 {
		t.Fatalf("em validação no Exemplo: %+v", emValidacao)
	}
	x := emValidacao.Tarefas[0]
	if x.Id != validar.Id || x.Titulo != "Revisar o contrato" || x.Status != api.EmValidacao || x.Organizacao != org ||
		x.SessaoAtiva == nil || *x.SessaoAtiva != s.Sessao.Id || x.Aguardando == nil || *x.Aguardando != "aprovar o PR" {
		t.Fatalf("resumo: %+v", x)
	}
	if got := ids(consultar("status=em_validacao,bloqueada")); !slices.Equal(got, []string{validar.Id, bloquear.Id, outra.Id}) {
		t.Fatalf("vários status: %v", got)
	}
	if got := ids(consultar("requisito=" + r.Id)); !slices.Equal(got, []string{outra.Id}) {
		t.Fatalf("por requisito: %v", got)
	}
	if got := ids(consultar("componente=" + c.Id)); !slices.Equal(got, []string{outra.Id}) {
		t.Fatalf("por componente: %v", got)
	}
	limitada := consultar("organizacao=Exemplo&limite=2")
	if len(limitada.Tarefas) != 2 || limitada.Total != 3 || limitada.Fora != 1 {
		t.Fatalf("limite: %+v", limitada)
	}
	var bruto map[string]any
	a.chamar("GET", "/busca/tarefas?organizacao=Exemplo", "", nil, 200, &bruto)
	texto, _ := json.Marshal(bruto)
	if strings.Contains(string(texto), "longa") || strings.Contains(string(texto), "resumo") {
		t.Fatalf("a consulta deve ser compacta: %s", texto)
	}
}

func TestRequisitoObsoleto(t *testing.T) {
	a := novoAmbiente(t)
	org := "Exemplo"
	var zero, copia, antigo, novo api.Requisito
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Zero", Organizacao: &org}, 201, &zero)
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Cópia fora de casa"}, 201, &copia)
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Backup do serviço antigo", DerivaDe: &[]string{copia.Id}}, 201, &antigo)
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Backup do serviço novo", DerivaDe: &[]string{copia.Id}}, 201, &novo)
	dias := 30
	a.chamar("POST", "/requisitos/"+novo.Id+"/execucoes", "", api.NovaExecucao{Evidencia: "restaurado", ValidadeDias: &dias}, 201, nil)
	tarefa := a.tarefa("Testar o backup antigo")
	a.chamar("POST", "/tarefas/"+tarefa.Id+"/requisitos", "", api.VinculoRequisito{Requisito: antigo.Id}, 200, nil)
	obter := func(id string) api.Requisito {
		var r api.Requisito
		a.chamar("GET", "/requisitos/"+id, "", nil, 200, &r)
		return r
	}
	if c := obter(copia.Id); c.Estado != api.NaoAtendido || !slices.Equal(c.LimitadoPor, []string{antigo.Id}) {
		t.Fatalf("antes, o antigo limita o pai: %+v", c)
	}

	// Motivo obrigatório; o zero não fica obsoleto.
	a.chamar("PUT", "/requisitos/"+antigo.Id+"/obsolescencia", "", api.Obsolescencia{Motivo: " "}, 400, nil)
	a.chamar("PUT", "/requisitos/"+zero.Id+"/obsolescencia", "", api.Obsolescencia{Motivo: "x"}, 409, nil)
	var marcado api.Requisito
	a.chamar("PUT", "/requisitos/"+antigo.Id+"/obsolescencia", "", api.Obsolescencia{Motivo: "o serviço não existe mais"}, 200, &marcado)
	if marcado.Obsoleto == nil || marcado.Obsoleto.Motivo != "o serviço não existe mais" || marcado.Peso != 0 {
		t.Fatalf("obsoleto com data e motivo, sem peso: %+v", marcado)
	}
	if len(marcado.Atencao) != 1 || marcado.Atencao[0].Motivo != api.ObsoletoComTarefaAberta {
		t.Fatalf("a tarefa aberta pede decisão: %+v", marcado.Atencao)
	}
	a.chamar("PUT", "/requisitos/"+antigo.Id+"/obsolescencia", "", api.Obsolescencia{Motivo: "de novo"}, 409, nil)

	// O pai passa a vir só do que sobra; o irmão vivo fica com a fatia inteira.
	if c := obter(copia.Id); c.Estado != api.Validado || len(c.LimitadoPor) != 0 {
		t.Fatalf("o pai não é mais limitado pelo obsoleto: %+v", c)
	}
	if z := obter(zero.Id); z.Estado != api.Validado {
		t.Fatalf("a cadeia até o zero: %+v", z)
	}
	if n := obter(novo.Id); !perto(n.Importancia[0].Fatia, 1) {
		t.Fatalf("o irmão vivo fica com a fatia inteira: %+v", n.Importancia)
	}

	// A tarefa aberta fica sinalizada; concluída, deixa de ficar.
	var lida api.Tarefa
	a.chamar("GET", "/tarefas/"+tarefa.Id, "", nil, 200, &lida)
	if lida.RequisitosObsoletos == nil || !slices.Equal(*lida.RequisitosObsoletos, []string{antigo.Id}) {
		t.Fatalf("tarefa sinalizada: %+v", lida.RequisitosObsoletos)
	}
	var lista []api.Tarefa
	a.chamar("GET", "/tarefas", "", nil, 200, &lista)
	if lista[0].RequisitosObsoletos == nil {
		t.Fatalf("sinalizada também na lista: %+v", lista[0])
	}

	// Fora da busca e da árvore padrão; com incluir_obsoletos, volta marcado.
	var achados []api.ResumoDeRequisito
	a.chamar("GET", "/busca/requisitos?q=backup", "", nil, 200, &achados)
	if len(achados) != 1 || achados[0].Id != novo.Id {
		t.Fatalf("busca sem obsoletos: %+v", achados)
	}
	a.chamar("GET", "/busca/requisitos?q=backup&incluir_obsoletos=true", "", nil, 200, &achados)
	if len(achados) != 2 {
		t.Fatalf("busca com obsoletos: %+v", achados)
	}
	var arvore api.ArvoreDeRequisitos
	a.chamar("GET", "/arvore/requisitos", "", nil, 200, &arvore)
	if strings.Contains(arvore.Texto, antigo.Id) || arvore.Requisitos != 3 {
		t.Fatalf("árvore sem obsoletos: %q", arvore.Texto)
	}
	a.chamar("GET", "/arvore/requisitos?incluir_obsoletos=true", "", nil, 200, &arvore)
	if !strings.Contains(arvore.Texto, antigo.Id+" [? pts · 0,0% · 0,0% do total] [obsoleto] ") {
		t.Fatalf("árvore com obsoletos: %q", arvore.Texto)
	}

	// Nada novo se liga a ele.
	a.chamar("POST", "/requisitos", "", api.NovoRequisito{Titulo: "Filho", DerivaDe: &[]string{antigo.Id}}, 409, nil)
	outra := a.tarefa("Outra")
	a.chamar("POST", "/tarefas/"+outra.Id+"/requisitos", "", api.VinculoRequisito{Requisito: antigo.Id}, 409, nil)
	a.chamar("DELETE", "/tarefas/"+tarefa.Id+"/requisitos/"+antigo.Id, "", nil, 200, nil)
	if r := obter(antigo.Id); len(r.Atencao) != 0 {
		t.Fatalf("sem tarefa aberta, nada a decidir: %+v", r.Atencao)
	}

	// Reativar desfaz; o histórico fica nos eventos.
	var reativado api.Requisito
	a.chamar("DELETE", "/requisitos/"+antigo.Id+"/obsolescencia", "", nil, 200, &reativado)
	if reativado.Obsoleto != nil || reativado.Peso == 0 {
		t.Fatalf("reativado: %+v", reativado)
	}
	a.chamar("DELETE", "/requisitos/"+antigo.Id+"/obsolescencia", "", nil, 409, nil)
	var eventos []api.Evento
	a.chamar("GET", "/eventos?entidade="+antigo.Id, "", nil, 200, &eventos)
	tipos := []string{}
	for _, e := range eventos {
		tipos = append(tipos, e.Tipo)
	}
	if !slices.Contains(tipos, "requisito_obsoleto") || !slices.Contains(tipos, "requisito_reativado") {
		t.Fatalf("eventos: %v", tipos)
	}
}
