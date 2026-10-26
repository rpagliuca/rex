package lancador

import (
	"strings"
	"testing"

	"github.com/rpagliuca/rex/api"
	"github.com/rpagliuca/rex/internal/servidor"
)

// tmuxFalso simula as sessões tmux: nome → tarefa do ambiente.
type tmuxFalso struct {
	sessoes  map[string]string
	chamadas []string
}

func (f *tmuxFalso) executar(args ...string) (string, error) {
	f.chamadas = append(f.chamadas, strings.Join(args, " "))
	alvo := func() string {
		for i, a := range args {
			if a == "-t" {
				return strings.TrimSuffix(strings.TrimPrefix(args[i+1], "="), ":")
			}
		}
		return ""
	}
	switch args[0] {
	case "list-sessions":
		var nomes []string
		for n := range f.sessoes {
			nomes = append(nomes, n)
		}
		return strings.Join(nomes, "\n"), nil
	case "show-environment":
		// O valor é a tarefa (em VariavelTarefa) ou "VARIAVEL=tarefa", para simular outra variável.
		if t, ok := f.sessoes[alvo()]; ok && t != "" {
			variavel, id := VariavelTarefa, t
			if v, i, achou := strings.Cut(t, "="); achou {
				variavel, id = v, i
			}
			if variavel == args[len(args)-1] {
				return variavel + "=" + id, nil
			}
		}
		return "", errFalso
	case "has-session":
		if _, ok := f.sessoes[alvo()]; ok {
			return "", nil
		}
		return "", errFalso
	case "new-session":
		nome, tarefa := "", ""
		for i, a := range args {
			if a == "-s" {
				nome = args[i+1]
			}
			if a == "-e" {
				tarefa = strings.TrimPrefix(args[i+1], VariavelTarefa+"=")
			}
		}
		f.sessoes[nome] = tarefa
	case "list-panes":
		return "500 manual\n600 outra", nil
	case "kill-session":
		delete(f.sessoes, alvo())
	case "rename-session":
		f.sessoes[args[len(args)-1]] = f.sessoes[alvo()]
		delete(f.sessoes, alvo())
	}
	return "", nil
}

type erroFalso struct{}

func (erroFalso) Error() string { return "falso" }

var errFalso = erroFalso{}

func novoLancador(t *testing.T) (*Lancador, *tmuxFalso) {
	t.Helper()
	l, err := Novo(Config{Diretorio: "/journals", Comando: []string{"agente", "--nome", "{nome}"}})
	if err != nil {
		t.Fatal(err)
	}
	f := &tmuxFalso{sessoes: map[string]string{"outra": ""}}
	l.executar = f.executar
	return l, f
}

func ptr(s string) *string { return &s }

func TestSobeAgenteAoEntrarEmAndamento(t *testing.T) {
	l, f := novoLancador(t)
	tarefa := api.Tarefa{Id: "T-0007", Titulo: "Revisão do backup de fotos", Status: api.EmAndamento}

	// Uma sessão que põe a própria tarefa em andamento não ganha outro agente.
	l.MudouStatus(servidor.MudancaDeStatus{Tarefa: tarefa, Anterior: api.Pausada, Autor: "S-0001"})
	if len(f.sessoes) != 1 {
		t.Fatalf("subiu agente para mudança feita por sessão: %v", f.chamadas)
	}

	l.MudouStatus(servidor.MudancaDeStatus{Tarefa: tarefa, Anterior: api.Pausada, Autor: "local"})
	if f.sessoes["backup-fotos"] != "T-0007" {
		t.Fatalf("agente não subiu como esperado: %v", f.sessoes)
	}
	ultima := f.chamadas[len(f.chamadas)-1]
	if !strings.Contains(ultima, "-c /journals") || !strings.Contains(ultima, "agente --nome backup-fotos") ||
		!strings.Contains(ultima, "tarefa T-0007") {
		t.Fatalf("comando inesperado: %s", ultima)
	}

	// Um tmux que sobrou da tarefa é fechado antes de subir o agente novo.
	tarefa.SessaoAtiva = nil
	l.MudouStatus(servidor.MudancaDeStatus{Tarefa: tarefa, Anterior: api.Concluida, Autor: "local"})
	if len(f.sessoes) != 2 || f.sessoes["backup-fotos"] != "T-0007" {
		t.Fatalf("tmux antigo não foi substituído: %v", f.sessoes)
	}

	// Com sessão ativa, não sobe outro.
	tarefa.SessaoAtiva = ptr("S-0002")
	l.MudouStatus(servidor.MudancaDeStatus{Tarefa: tarefa, Anterior: api.AFazer, Autor: "local"})
	if len(f.sessoes) != 2 {
		t.Fatalf("subiu agente para tarefa com sessão ativa: %v", f.sessoes)
	}
}

func TestAvisaAgenteAoSairDeAndamento(t *testing.T) {
	l, f := novoLancador(t)
	f.sessoes["backup"] = "T-0007"
	tarefa := api.Tarefa{Id: "T-0007", Titulo: "Backup", Status: api.Pausada, SessaoAtiva: ptr("S-0002")}

	// A própria sessão mudando o status: nada a avisar.
	l.MudouStatus(servidor.MudancaDeStatus{Tarefa: tarefa, Anterior: api.EmAndamento, Autor: "S-0002"})
	for _, c := range f.chamadas {
		if strings.HasPrefix(c, "send-keys") {
			t.Fatalf("avisou o agente da própria mudança: %v", f.chamadas)
		}
	}

	l.MudouStatus(servidor.MudancaDeStatus{Tarefa: tarefa, Anterior: api.EmAndamento, Autor: "local"})
	avisos := 0
	for _, c := range f.chamadas {
		if strings.HasPrefix(c, "send-keys -t =backup: -l") && strings.Contains(c, "pausada") {
			avisos++
		}
	}
	if avisos != 1 {
		t.Fatalf("aviso não enviado: %v", f.chamadas)
	}

}

func TestAvisaAgenteSubidoAntesDaRenomeacao(t *testing.T) {
	l, f := novoLancador(t)
	f.sessoes["antigo"] = VariavelTarefaLegada + "=T-0008"
	tarefa := api.Tarefa{Id: "T-0008", Titulo: "Antigo", Status: api.Pausada, SessaoAtiva: ptr("S-0003")}

	l.MudouStatus(servidor.MudancaDeStatus{Tarefa: tarefa, Anterior: api.EmAndamento, Autor: "local"})
	for _, c := range f.chamadas {
		if strings.HasPrefix(c, "send-keys -t =antigo: -l") {
			return
		}
	}
	t.Fatalf("o agente com REQUERIAL_TAREFA não foi avisado: %v", f.chamadas)
}

func TestNomeCurto(t *testing.T) {
	casos := map[string]string{
		"Revisão do backup de fotos":    "backup-fotos",
		"Usina — causa raiz das perdas": "usina-causa-raiz-perdas",
		"AD":                            "tarefa",
		"Retomar implantação de Samba com Active Directory (AD) no cartório": "samba-active-directory",
		"Criar atalhos da Siri": "atalhos-siri",
		"Revisar proposta":      "proposta",
	}
	for titulo, esperado := range casos {
		if obtido := nomeCurto(titulo); obtido != esperado {
			t.Errorf("nomeCurto(%q) = %q, esperado %q", titulo, obtido, esperado)
		}
	}
}

func TestLancamentoComModeloEsforcoEInstrucao(t *testing.T) {
	l, err := Novo(Config{
		Diretorio: "/journals", Comando: []string{"agente"},
		Modelos:           []Modelo{{Nome: "Modelo A", Argumentos: []string{"--model", "a"}}, {Nome: "Modelo B", Argumentos: []string{"--model", "b"}}},
		Esforcos:          []string{"low", "high"},
		ArgumentosEsforco: []string{"--effort", "{esforco}"},
		RemoverDoAmbiente: []string{"CLAUDE_EFFORT"},
		Ambiente:          map[string]string{"CLAUDE_EFFORT": "{esforco}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	f := &tmuxFalso{sessoes: map[string]string{}}
	l.executar = f.executar

	ruim := api.PedidoDeLancamento{Modelo: ptr("Modelo Z")}
	if l.Validar(&ruim) == nil {
		t.Fatal("aceitou modelo desconhecido")
	}
	pedido := api.PedidoDeLancamento{Modelo: ptr("Modelo B"), Esforco: ptr("low"), Instrucao: ptr("Comece pelo teste que falhou.")}
	if err := l.Validar(&pedido); err != nil {
		t.Fatal(err)
	}
	// Relançar uma tarefa que já estava em andamento, sem agente, também vale.
	tarefa := api.Tarefa{Id: "T-0009", Titulo: "Corrigir importação", Status: api.EmAndamento}
	l.MudouStatus(servidor.MudancaDeStatus{Tarefa: tarefa, Anterior: api.EmAndamento, Autor: "local", Lancamento: &pedido})
	ultima := f.chamadas[len(f.chamadas)-1]
	if !strings.Contains(ultima, "env -u CLAUDE_EFFORT CLAUDE_EFFORT=low agente --model b --effort low") || !strings.Contains(ultima, "Comece pelo teste que falhou.") {
		t.Fatalf("comando inesperado: %s", ultima)
	}

	padrao := api.PedidoDeLancamento{}
	if err := l.Validar(&padrao); err != nil || *padrao.Modelo != "Modelo A" || padrao.Esforco != nil {
		t.Fatalf("padrões inesperados: %+v %v", padrao, err)
	}
}

type sessoesFalsas struct {
	abertas    []string
	encerradas []string
}

func (s *sessoesFalsas) AbrirSessaoLancada(tarefa, agente, servidor, chat, nome string) (api.SessaoCriada, error) {
	s.abertas = append(s.abertas, tarefa+"|"+agente+"|"+chat+"|"+nome)
	return api.SessaoCriada{Sessao: api.Sessao{Id: "S-0042"}, Token: "segredo"}, nil
}

func (s *sessoesFalsas) Sessao(id string) (api.Sessao, error) {
	chat := "chat-manual"
	return api.Sessao{Id: id, Chat: &chat}, nil
}

func (s *sessoesFalsas) EncerrarSessaoLancada(id, resumo string) error {
	s.encerradas = append(s.encerradas, id)
	return nil
}

func TestLancadorAbreASessaoAntesDoAgente(t *testing.T) {
	l, err := Novo(Config{
		Diretorio: "/journals", Comando: []string{"agente", "--session-id", "{chat}"},
		Modelos: []Modelo{{Nome: "Claude Opus 5.5", Argumentos: []string{"--model", "opus"}}}, EsforcoPadrao: "medium",
	})
	if err != nil {
		t.Fatal(err)
	}
	f := &tmuxFalso{sessoes: map[string]string{}}
	l.executar = f.executar
	sessoes := &sessoesFalsas{}
	l.sessoes = sessoes

	l.MudouStatus(servidor.MudancaDeStatus{Tarefa: api.Tarefa{Id: "T-0001", Titulo: "Backup", Status: api.EmAndamento}, Anterior: api.AFazer, Autor: "local"})
	if len(sessoes.abertas) != 1 {
		t.Fatalf("sessão não aberta: %v", sessoes.abertas)
	}
	partes := strings.Split(sessoes.abertas[0], "|")
	if partes[0] != "T-0001" || partes[1] != "Claude Opus 5.5 Medium" || len(partes[2]) != 36 || partes[3] != "backup" {
		t.Fatalf("sessão aberta com dados inesperados: %v", partes)
	}
	ultima := f.chamadas[len(f.chamadas)-1]
	if !strings.Contains(ultima, "-e "+VariavelSessao+"=S-0042") || !strings.Contains(ultima, "-e "+VariavelToken+"=segredo") ||
		!strings.Contains(ultima, "--session-id "+partes[2]) {
		t.Fatalf("o agente não recebeu a sessão: %s", ultima)
	}
}

func TestAvisaAgenteIniciadoAMaoPeloChat(t *testing.T) {
	l, f := novoLancador(t)
	f.sessoes["manual"] = "" // tmux sem REX_TAREFA: o lançador não subiu este agente
	l.sessoes = &sessoesFalsas{}
	// Navegador órfão que o agente abriu (pid 300 → init) vem antes; depois, MCP (pid 700) → claude
	// (650) → shell do painel (500).
	l.processosDoChat = func(chat string) []int {
		if chat == "chat-manual" {
			return []int{300, 700}
		}
		return nil
	}
	l.paiDe = func(pid int) int { return map[int]int{300: 1, 700: 650, 650: 500}[pid] }

	tarefa := api.Tarefa{Id: "T-0009", Titulo: "Alertas", Status: api.Pausada, SessaoAtiva: ptr("S-0010")}
	l.MudouStatus(servidor.MudancaDeStatus{Tarefa: tarefa, Anterior: api.EmAndamento, Autor: "local"})
	for _, c := range f.chamadas {
		if strings.HasPrefix(c, "send-keys -t =manual: -l") {
			return
		}
	}
	t.Fatalf("o agente iniciado à mão não foi avisado: %v", f.chamadas)
}

func TestNomeDadoPorQuemLanca(t *testing.T) {
	l, err := Novo(Config{Diretorio: "/journals", Comando: []string{"agente", "--remote-control", "{nome}"},
		Modelos: []Modelo{{Nome: "Modelo A"}}})
	if err != nil {
		t.Fatal(err)
	}
	f := &tmuxFalso{sessoes: map[string]string{}}
	l.executar = f.executar
	sessoes := &sessoesFalsas{}
	l.sessoes = sessoes

	vazio := api.PedidoDeLancamento{Nome: ptr("—")}
	if l.Validar(&vazio) == nil {
		t.Fatal("aceitou nome sem letra nem número")
	}
	pedido := api.PedidoDeLancamento{Nome: ptr("Dados-privado do João")}
	if err := l.Validar(&pedido); err != nil || *pedido.Nome != "dados-privado-do-joao" {
		t.Fatalf("nome não normalizado: %v %v", pedido.Nome, err)
	}
	tarefa := api.Tarefa{Id: "T-0136", Titulo: "Retomar implantação de Samba com Active Directory (AD) no cartório", Status: api.EmAndamento}
	l.MudouStatus(servidor.MudancaDeStatus{Tarefa: tarefa, Anterior: api.Pausada, Autor: "local", Lancamento: &pedido})
	if _, ok := f.sessoes["dados-privado-do-joao"]; !ok {
		t.Fatalf("tmux sem o nome dado: %v", f.sessoes)
	}
	if !strings.HasSuffix(sessoes.abertas[0], "|dados-privado-do-joao") ||
		!strings.Contains(f.chamadas[len(f.chamadas)-1], "--remote-control dados-privado-do-joao") {
		t.Fatalf("nome não chegou à sessão e ao agente: %v %s", sessoes.abertas, f.chamadas[len(f.chamadas)-1])
	}
}

func TestRenomeiaOAgenteVivo(t *testing.T) {
	l, f := novoLancador(t)
	l.sessoes = &sessoesFalsas{}
	l.processosDoChat = func(string) []int { return nil }
	f.sessoes["retomar-implantacao"] = "T-0136"

	l.Renomeou(api.Sessao{Id: "S-0185", Nome: ptr("dados-privado-joao"), Tarefas: []string{"T-0136"}})
	if f.sessoes["dados-privado-joao"] != "T-0136" {
		t.Fatalf("tmux não renomeado: %v", f.sessoes)
	}
	n := len(f.chamadas)
	if f.chamadas[n-2] != "send-keys -t =dados-privado-joao: -l /rename dados-privado-joao" ||
		f.chamadas[n-1] != "send-keys -t =dados-privado-joao: Enter" {
		t.Fatalf("/rename não enviado: %v", f.chamadas)
	}

	// Mesmo nome: só reaplica o /rename, sem mexer no tmux.
	l.Renomeou(api.Sessao{Id: "S-0185", Nome: ptr("dados-privado-joao"), Tarefas: []string{"T-0136"}})
	for _, c := range f.chamadas[n:] {
		if strings.HasPrefix(c, "rename-session") {
			t.Fatalf("renomeou o tmux sem precisar: %v", f.chamadas[n:])
		}
	}
}
