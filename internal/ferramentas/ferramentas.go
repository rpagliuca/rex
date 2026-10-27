// Package ferramentas expõe a API do rex como servidor MCP para agentes de IA.
//
// É uma camada fina sobre a API HTTP: quem escreve no banco continua sendo só o servidor.
// Cada processo MCP corresponde a um chat do agente e guarda o token da sessão aberta nele;
// o token nunca aparece nas respostas, e toda escrita feita depois de iniciar a sessão é
// atribuída a ela.
package ferramentas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rpagliuca/rex/api"
)

// Ferramentas guarda o cliente da API e a sessão aberta neste processo.
type Ferramentas struct {
	cliente *api.Client
	// ambiente lê as variáveis que o cliente do agente passa ao processo MCP, como
	// CLAUDE_EFFORT e CLAUDE_CODE_SESSION_ID. Substituível nos testes.
	ambiente func(string) string
	// journals é a raiz onde cada sessão ganha a pasta do seu journal; vazio desliga.
	journals string
	// ouvir marca as novidades de voz_situacao como ouvidas: só o conector do app do Claude, que fala
	// o que recebe ao Rafael. Nas ferramentas de um agente, ler não muda nada.
	ouvir bool

	mu     sync.Mutex
	sessao string
	token  string
	// adotada diz que a sessão veio pronta do lançador (REX_SESSAO e REX_TOKEN), e
	// conferida indica que o ambiente já foi lido.
	adotada   bool
	conferida bool
}

// Novo cria as ferramentas apontando para a API no endereço dado.
func Novo(endereco string) (*Ferramentas, error) {
	cliente, err := api.NewClient(endereco)
	if err != nil {
		return nil, err
	}
	return &Ferramentas{cliente: cliente, ambiente: os.Getenv}, nil
}

// NovoEmProcesso cria as ferramentas chamando a API direto no handler, sem rede: é o caso do
// conector remoto, servido pelo próprio rex.
func NovoEmProcesso(h http.Handler) *Ferramentas {
	cliente, _ := api.NewClient("http://rex", api.WithHTTPClient(emProcesso{h}))
	return &Ferramentas{cliente: cliente, ambiente: func(string) string { return "" }, ouvir: true}
}

type emProcesso struct{ h http.Handler }

func (e emProcesso) Do(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w.Result(), nil
}

// ComJournals faz cada sessão iniciada sem journal ganhar a pasta <dir>/<S-0001>/journal.md.
func (f *Ferramentas) ComJournals(dir string) *Ferramentas {
	f.journals = dir
	return f
}

// Servidor monta o servidor MCP com todas as ferramentas registradas.
func (f *Ferramentas) Servidor() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "rex", Version: "0.1.0"}, &mcp.ServerOptions{
		Instructions: "Tarefas e Sessões de Trabalho do rex. Regra: toda sessão de trabalho é registrada " +
			"antes da primeira ação que muda estado, qualquer que seja a origem do chat (carregue a skill rex). " +
			"Comece por sessao_atual (o lançador pode já ter aberto a sua); senão, iniciar_sessao na tarefa, ou sem " +
			"tarefa e depois criar_tarefa + vincular_tarefa. Ao terminar, encerrar_sessao com um resumo para o próximo agente.",
	})

	mcp.AddTool(s, &mcp.Tool{Name: "obter_tarefa", Description: descricoes["obter_tarefa"]}, f.obterTarefa)
	mcp.AddTool(s, &mcp.Tool{Name: "consultar_tarefas", Description: descricoes["consultar_tarefas"]}, f.consultarTarefas)
	mcp.AddTool(s, &mcp.Tool{Name: "criar_tarefa", Description: descricoes["criar_tarefa"]}, f.criarTarefa)
	mcp.AddTool(s, &mcp.Tool{Name: "alterar_tarefa", Description: "Altera campos de uma tarefa."}, f.alterarTarefa)
	mcp.AddTool(s, &mcp.Tool{Name: "historico_da_tarefa", Description: "Lista as sessões que trabalharam na tarefa, da mais recente para a mais antiga."}, f.historicoDaTarefa)
	mcp.AddTool(s, &mcp.Tool{Name: "iniciar_sessao", Description: "Abre a Sessão de Trabalho deste chat, nas tarefas dadas ou sem nenhuma (vincule depois). Cria a pasta do journal. Recusa tarefa que já tem sessão ativa."}, f.iniciarSessao)
	mcp.AddTool(s, &mcp.Tool{Name: "sessao_atual", Description: "Mostra a Sessão de Trabalho aberta neste chat."}, f.sessaoAtual)
	mcp.AddTool(s, &mcp.Tool{Name: "vincular_tarefa", Description: "Liga mais uma tarefa à sessão deste chat."}, f.vincularTarefa)
	mcp.AddTool(s, &mcp.Tool{Name: "atualizar_journal", Description: "Atualiza os localizadores do journal e dos artefatos da sessão deste chat."}, f.atualizarJournal)
	mcp.AddTool(s, &mcp.Tool{Name: "renomear_sessao", Description: "Troca o nome curto deste agente (tmux e remote-control, o que o Rafael vê na lista do Claude Code web). Use quando o nome não disser mais o assunto do trabalho."}, f.renomearSessao)
	mcp.AddTool(s, &mcp.Tool{Name: "encerrar_sessao", Description: "Encerra a sessão deste chat e grava o resumo em cada tarefa dela."}, f.encerrarSessao)
	mcp.AddTool(s, &mcp.Tool{Name: "repassar_sessao", Description: "Troca de agente: encerra a sessão deste chat e abre outra com o agente novo, nas mesmas tarefas."}, f.repassarSessao)
	mcp.AddTool(s, &mcp.Tool{Name: "buscar_requisitos", Description: "Busca requisitos por palavras (título pesa mais; sem acento; prefixo). Devolve só id, título, estado e componentes dos mais relevantes. Use antes de criar um requisito ou ligar uma tarefa."}, f.buscarRequisitos)
	mcp.AddTool(s, &mcp.Tool{Name: "arvore_de_requisitos", Description: "A cadeia de requisitos em texto (id, [pontos · fatia entre irmãos · peso no total] e título, indentada; irmãos do mais ao menos importante), a partir do zero da organização; com componente, só os dele e os ancestrais. Use para escolher o pai de um requisito novo ou para a triagem por pontos: custa mais que buscar."}, f.arvoreDeRequisitos)
	mcp.AddTool(s, &mcp.Tool{Name: "obter_requisito", Description: "Obtém um requisito: trilhas até o zero (em títulos), origem, cadeia (deriva_de acima, derivados abaixo), componentes e tarefas que tentam atendê-lo."}, f.obterRequisito)
	mcp.AddTool(s, &mcp.Tool{Name: "criar_requisito", Description: "Cria um requisito, com origem e os requisitos dos quais deriva (a organização vem do primeiro, se omitida)."}, f.criarRequisito)
	mcp.AddTool(s, &mcp.Tool{Name: "alterar_requisito", Description: "Altera título, descrição, organização ou origem de um requisito. A importância não fica aqui: é dada em pontos na ligação com cada pai (definir_pontos)."}, f.alterarRequisito)
	mcp.AddTool(s, &mcp.Tool{Name: "derivar_requisito", Description: "Registra que um requisito deriva de outro (um requisito pode derivar de vários). Recusa ciclos. Pontos opcionais; ao derivar de mais um pai, o padrão é 1."}, f.derivarRequisito)
	mcp.AddTool(s, &mcp.Tool{Name: "definir_pontos", Description: "Triagem: dá ao requisito os pontos de importância sob um pai. Os pontos só se comparam entre irmãos: a fatia é pontos / soma dos irmãos, e o peso no total soma, sobre os pais, peso do pai × fatia. Sem pontos, volta a não triado (conta como 1)."}, f.definirPontos)
	mcp.AddTool(s, &mcp.Tool{Name: "marcar_obsoleto", Description: "Marca um requisito como obsoleto, com o motivo (nada é apagado): ele deixa de limitar o estado dos pais e de pesar entre os irmãos, sai da busca e da árvore padrão, e as tarefas abertas dele ficam sinalizadas para decisão. Com reativar, desfaz (marcado por engano)."}, f.marcarObsoleto)
	mcp.AddTool(s, &mcp.Tool{Name: "vincular_requisito", Description: "Registra que uma tarefa tenta atender um requisito."}, f.vincularRequisito)
	mcp.AddTool(s, &mcp.Tool{Name: "registrar_execucao", Description: "Registra uma execução de conformidade: o requisito foi verificado e está atendido, com a evidência e a validade em dias (depois dela, a validação expira). Só registre o que você verificou de fato."}, f.registrarExecucao)
	mcp.AddTool(s, &mcp.Tool{Name: "listar_componentes", Description: "Lista os componentes (repositórios, módulos, lugares, funcionalidades…) com os requisitos e tarefas de cada um, para reaproveitar em vez de duplicar."}, f.listarComponentes)
	mcp.AddTool(s, &mcp.Tool{Name: "criar_componente", Description: "Cria um componente: qualquer coisa reaproveitável a que requisitos e tarefas se referem."}, f.criarComponente)
	mcp.AddTool(s, &mcp.Tool{Name: "vincular_componente", Description: "Liga um componente a um requisito ou a uma tarefa (informe um dos dois)."}, f.vincularComponente)
	mcp.AddTool(s, &mcp.Tool{Name: "opcoes_do_lancador", Description: descricoes["opcoes_do_lancador"]}, f.opcoesDoLancador)
	mcp.AddTool(s, &mcp.Tool{Name: "lancar_agente", Description: descricoes["lancar_agente"]}, f.lancarAgente)
	mcp.AddTool(s, &mcp.Tool{Name: "agentes_ativos", Description: "Sessões de trabalho ativas agora: id, agente, tarefas e início. Para saber quem está trabalhando em quê."}, f.agentesAtivos)
	mcp.AddTool(s, &mcp.Tool{Name: "listar_templates", Description: "Tarefas pré-moldadas (templates): id, título, organização, periodicidade, se está devida e a instância aberta."}, f.listarTemplates)
	mcp.AddTool(s, &mcp.Tool{Name: "instanciar_template", Description: "Cria uma tarefa (a fazer) a partir de uma pré-moldada, com o contexto desta vez no topo da descrição e os requisitos dela. Não sobe agente: para isso, lancar_agente na tarefa criada."}, f.instanciarTemplate)
	mcp.AddTool(s, &mcp.Tool{Name: "listar_origens", Description: "Lista as origens de requisitos (pessoas e documentos), para reaproveitar em vez de duplicar."}, f.listarOrigens)
	mcp.AddTool(s, &mcp.Tool{Name: "criar_origem", Description: "Cria uma origem de requisitos: uma pessoa (quem pede) ou um documento (lei, norma, contrato)."}, f.criarOrigem)
	mcp.AddTool(s, &mcp.Tool{Name: "criar_lembrete", Description: descricoes["criar_lembrete"]}, f.criarLembrete)
	mcp.AddTool(s, &mcp.Tool{Name: "proximos_lembretes", Description: descricoes["proximos_lembretes"]}, f.proximosLembretes)
	mcp.AddTool(s, &mcp.Tool{Name: "adiar_lembrete", Description: descricoes["adiar_lembrete"]}, f.adiarLembrete)
	mcp.AddTool(s, &mcp.Tool{Name: "alterar_lembrete", Description: descricoes["alterar_lembrete"]}, f.alterarLembrete)
	mcp.AddTool(s, &mcp.Tool{Name: "confirmar_lembrete", Description: descricoes["confirmar_lembrete"]}, f.confirmarLembrete)
	mcp.AddTool(s, &mcp.Tool{Name: "cancelar_lembrete", Description: descricoes["cancelar_lembrete"]}, f.cancelarLembrete)
	mcp.AddTool(s, &mcp.Tool{Name: "aguardar_resposta", Description: "Avisa que a sessão deste chat entregou ou está bloqueada e espera o Rafael (aprovação, decisão, informação). Aparece nas pendências até retomar_trabalho ou o encerramento."}, f.aguardarResposta)
	mcp.AddTool(s, &mcp.Tool{Name: "retomar_trabalho", Description: "Tira a sessão deste chat da espera: o Rafael respondeu e o trabalho continuou."}, f.retomarTrabalho)
	mcp.AddTool(s, &mcp.Tool{Name: "voz_panorama", Description: "rex voz. As sessões ativas, cada uma com o que o agente faz agora (trabalhando, ocioso, pedindo_permissao, sem_agente), o que pediu ao Rafael (aguardando), quantas novidades ele não ouviu e o começo do último texto do agente; a tela e o texto inteiro vêm em voz_situacao. Responde \"quais sessões esperam meu input?\" numa chamada; não marca nada como ouvido."}, f.vozPanorama)
	mcp.AddTool(s, &mcp.Tool{Name: "voz_situacao", Description: "rex voz. Uma sessão (S-0001, ou a sessão ativa da tarefa T-0001): o que o agente faz agora e as novidades da conversa dele que o Rafael ainda não ouviu. Só leitura: nada fica como ouvido (isso é do conector do app do Claude). esperar_segundos segura até o agente responder ou parar."}, f.vozSituacao)
	mcp.AddTool(s, &mcp.Tool{Name: "enviar_a_sessao", Description: "Manda uma mensagem deste chat ao agente de outra sessão, para perguntar ou para responder. Ela chega identificada por esta sessão e com a instrução de responder com enviar_a_sessao para cá: a resposta entra neste chat como mensagem nova (não consulte voz_situacao nem leia o tmux dela para esperá-la). Com o agente trabalhando, entra na fila dele. Recusa quando ele espera uma escolha do Rafael na tela. Quando receber \"[rex · de S-… ]\", responda por aqui para essa sessão."}, f.enviarASessao)
	mcp.AddTool(s, &mcp.Tool{Name: "voz_enviar", Description: "rex voz. Escreve a frase do Rafael no agente da sessão, como pedido N; com o agente trabalhando, entra na fila dele. Recusa quando ele espera uma escolha na tela (use voz_tecla). Para uma sessão falar com outra, enviar_a_sessao."}, f.vozEnviar)
	mcp.AddTool(s, &mcp.Tool{Name: "voz_tecla", Description: "rex voz. Aperta, no agente da sessão, a opção que o Rafael escolheu numa tela de permissão (1 a 4, Enter) ou Escape para interromper o agente."}, f.vozTecla)
	mcp.AddTool(s, &mcp.Tool{Name: "listar_eventos", Description: "Lista o log de eventos, opcionalmente de uma entidade (T-0001, S-0001)."}, f.listarEventos)
	return s
}

// descricoes são as das ferramentas que os dois servidores (o do agente e o conector) oferecem.
var descricoes = map[string]string{
	"obter_tarefa":       "Obtém uma tarefa, incluindo o resumo para retomar e a sessão ativa.",
	"consultar_tarefas":  "Tarefas por status (um ou mais), organização, componente ou requisito; exige ao menos um filtro. Só id, título, status, organização, sessão ativa e o que ela espera do Rafael, da atualizada mais recente à mais antiga, até o limite (padrão 20, máximo 50); total diz quantas casaram e fora, quantas não vieram. Ex.: \"o que está em validação?\" é status [em_validacao]; \"o que tenho da Yes a fazer?\" é status [a_fazer] e organização Yes.",
	"criar_tarefa":       "Cria uma tarefa, já ligada aos requisitos e componentes informados.",
	"opcoes_do_lancador": "Modelos e esforços que o lançador de agentes aceita, e os padrões.",
	"lancar_agente":      "Sobe um agente novo (outro chat, em tmux) para trabalhar numa tarefa: põe a tarefa em andamento e abre a sessão de trabalho dele. Para delegar; nunca para a tarefa deste chat. Recusa tarefa com sessão ativa. Para parar o agente depois, mude o status da tarefa (alterar_tarefa).",
	"criar_lembrete":     "Agenda um lembrete para o Rafael, solto ou ligado a uma tarefa ou requisito. A precisão é obrigatória e vem do que o Rafael disse: hora só quando ele deu data e hora explícitas (\"às 14h\", \"9:30\"); turno para \"de manhã\", \"no almoço\", \"à tarde\", \"à noite\"; dia para \"amanhã\", \"sexta\", \"dia 30\" sem hora; semana para \"semana que vem\", \"na semana do dia 12\"; mes para \"em abril\", \"mês que vem\". Nunca invente uma hora: fora de hora, mande qualquer hora dentro da janela e o rex a troca pelo início dela. Só hora toca alarme; turno, dia, semana e mês são avisados em lote, sem som. Preencha curto com a palavra-chave do compromisso (até 10 caracteres, como ballet ou dentista): é o que cabe na ilha do iPhone.",
	"proximos_lembretes": "Lembretes que pedem atenção: os disparados e os que vencem nas próximas horas (padrão 24), com a hora de agora (agora), para calcular o quando de um lembrete novo. Cada um traz quando (início da janela), fim (fim da janela, fora da precisão hora) e situacao, e a lista vem nesta ordem: vencido (a janela já acabou: é o atrasado), na_janela (a janela está aberta: vale agora, não está atrasado) e futuro. Um lembrete de semana ou mês dispara na segunda ou no dia 1 às 8h e vale a semana ou o mês inteiro: com quando no passado e situacao na_janela, ele não é \"de ontem\".",
	"adiar_lembrete":     "Reagenda um lembrete para daqui a N minutos ou para uma hora (RFC 3339). Para pedidos em lote (\"os de ontem, joga para amanhã\"), adie só os de situacao vencido em proximos_lembretes. Lembrete de semana ou mês com a janela ainda aberta é recusado, porque ainda vale: só com mesmo_na_janela, quando o Rafael pediu aquele lembrete pelo nome.",
	"alterar_lembrete":   "Corrige texto, palavra-chave (curto), hora, precisão, organização ou ligações de um lembrete, mantendo o id: nunca cancele e recrie para isso. Só os campos informados mudam; tarefa ou requisito vazio remove a ligação. Confirmado ou cancelado não muda.",
	"confirmar_lembrete": "Marca o lembrete como visto: ele para de pedir atenção. Só quando o Rafael disser que viu ou fez.",
	"cancelar_lembrete":  "Cancela um lembrete que não vale mais. Só quando o Rafael disser que não vale mais; para mudar texto, hora ou ligações, alterar_lembrete.",
}

// anotacoesDoConector dizem ao app do Claude o que cada ferramenta faz, para ele não pedir
// confirmação a cada leitura.
var anotacoesDoConector = map[string]*mcp.ToolAnnotations{
	"obter_tarefa":       {ReadOnlyHint: true, Title: "Ver uma tarefa"},
	"consultar_tarefas":  {ReadOnlyHint: true, Title: "Consultar tarefas"},
	"criar_tarefa":       {DestructiveHint: ptr(false), Title: "Criar tarefa"},
	"opcoes_do_lancador": {ReadOnlyHint: true, Title: "Modelos e esforços do lançador"},
	"lancar_agente":      {DestructiveHint: ptr(false), Title: "Abrir sessão de trabalho"},
	"criar_lembrete":     {DestructiveHint: ptr(false), Title: "Criar lembrete"},
	"proximos_lembretes": {ReadOnlyHint: true, Title: "Próximos lembretes"},
	"adiar_lembrete":     {DestructiveHint: ptr(false), Title: "Adiar lembrete"},
	"alterar_lembrete":   {DestructiveHint: ptr(false), IdempotentHint: true, Title: "Alterar lembrete"},
	"confirmar_lembrete": {DestructiveHint: ptr(false), IdempotentHint: true, Title: "Confirmar lembrete"},
	"cancelar_lembrete":  {DestructiveHint: ptr(true), IdempotentHint: true, Title: "Cancelar lembrete"},
}

func doConector(nome string) *mcp.Tool {
	return &mcp.Tool{Name: nome, Description: descricoes[nome], Annotations: anotacoesDoConector[nome]}
}

// ServidorDoConector monta o servidor MCP do conector personalizado do Claude (/mcp, atrás de
// OAuth), para o app do Claude, no modo de voz, ser o rex voz: ver as
// sessões, ouvir o que disseram, mandar pedidos e responder às telas de permissão.
func (f *Ferramentas) ServidorDoConector() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "rex", Version: "0.1.0"}, &mcp.ServerOptions{
		Instructions: "Sessões de trabalho dos agentes de IA do Rafael no rex, quase sempre usadas por voz. " +
			"\"Quais sessões esperam meu input?\": voz_panorama; esperam as com aguardando, as em pedindo_permissao e as ociosas com nao_ouvidas. " +
			"\"O que a do deploy disse?\": voz_situacao e resuma as novidades. \"Diz pra ela…\": voz_enviar com a frase como ele disse, " +
			"depois voz_situacao com esperar_segundos 12 para contar a resposta. " +
			"Lembretes você mesmo gerencia (proximos_lembretes, criar_lembrete, alterar_lembrete, adiar_lembrete, confirmar_lembrete, cancelar_lembrete): " +
			"\"o que tenho amanhã?\" é proximos_lembretes com horas 48, \"esta semana\" 168; a precisão sai das palavras dele, nunca invente uma hora; " +
			"para criar, calcule o quando a partir do agora de proximos_lembretes; depois de criar ou alterar, diga o que ficou gravado (o rex põe a hora no início da janela). " +
			"Atrasado é só o de situacao vencido; os de semana e mês disparam na segunda ou no dia 1 e ficam na_janela até o fim dela, então \"os de ontem\" nunca os inclui. " +
			"Sessão de trabalho nova: criar_tarefa (título, organização Cartório, Yes ou Pessoal, descrição com o pedido nas palavras dele, status a_fazer) " +
			"e lancar_agente nela, com o nome curto do assunto; tarefa da Yes leva Work=<código> na primeira linha da descrição, e sem saber o código, " +
			"mande o pedido ao Agente Global. Consultas de tarefa: consultar_tarefas e obter_tarefa. Outro pedido sem sessão própria: voz_enviar ao Agente Global. " +
			"Na fala: curto, sem ids (chame cada sessão pelo nome curto lido como palavras), números por extenso. " +
			"Antes de voz_tecla, leia a tela (voz_situacao), diga as opções e aperte só a que ele escolher. " +
			"voz_panorama é leve (começo do último texto); o detalhe de uma sessão vem em voz_situacao.",
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        "voz_panorama",
		Description: "As sessões de trabalho ativas no rex, cada uma com o que o agente faz agora (trabalhando, ocioso, pedindo_permissao, sem_agente), o que pediu ao Rafael (aguardando), quantas novidades ele não ouviu e o começo do último texto do agente; a tela e o texto inteiro vêm em voz_situacao. Responde \"quais sessões esperam meu input?\" numa chamada. Só leitura.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, Title: "Panorama das sessões"},
	}, f.vozPanorama)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "voz_situacao",
		Description: "Uma sessão (S-0001, ou a sessão ativa da tarefa T-0001): o que o agente faz agora e as novidades da conversa dele que o Rafael ainda não ouviu, que ficam como ouvidas: resuma-as na resposta. esperar_segundos segura até o agente responder ou parar.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false), Title: "Situação de uma sessão"},
	}, f.vozSituacao)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "voz_enviar",
		Description: "Escreve a frase do Rafael no agente da sessão, como pedido N; com o agente trabalhando, entra na fila dele. Recusa quando ele espera uma escolha na tela (use voz_tecla).",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false), Title: "Mandar pedido a uma sessão"},
	}, f.vozEnviar)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "voz_tecla",
		Description: "Aperta, no agente da sessão, a opção que o Rafael escolheu numa tela de permissão (1 a 4, Enter) ou Escape para interromper o agente. Só depois de ele dizer qual.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(true), Title: "Responder tela de permissão"},
	}, f.vozTecla)
	// Lembretes e sessões novas o conector resolve sozinho: mandar ao agente global custa um turno
	// dele (~20 s). O autor é "local", como no frontend, então lancar_agente aciona o lançador.
	mcp.AddTool(s, doConector("proximos_lembretes"), f.proximosLembretes)
	mcp.AddTool(s, doConector("criar_lembrete"), f.criarLembrete)
	mcp.AddTool(s, doConector("alterar_lembrete"), f.alterarLembrete)
	mcp.AddTool(s, doConector("adiar_lembrete"), f.adiarLembrete)
	mcp.AddTool(s, doConector("confirmar_lembrete"), f.confirmarLembrete)
	mcp.AddTool(s, doConector("cancelar_lembrete"), f.cancelarLembrete)
	mcp.AddTool(s, doConector("consultar_tarefas"), f.consultarTarefas)
	mcp.AddTool(s, doConector("obter_tarefa"), f.obterTarefa)
	mcp.AddTool(s, doConector("criar_tarefa"), f.criarTarefa)
	mcp.AddTool(s, doConector("opcoes_do_lancador"), f.opcoesDoLancador)
	mcp.AddTool(s, doConector("lancar_agente"), f.lancarAgente)
	return s
}

func ptr[T any](v T) *T { return &v }

// autorizar acrescenta o token da sessão aberta, se houver, para atribuir a escrita a ela.
func (f *Ferramentas) autorizar(ctx context.Context, req *http.Request) error {
	f.adotar(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.token != "" {
		req.Header.Set("Authorization", "Bearer "+f.token)
	}
	return nil
}

// variavel lê REX_<nome> do ambiente e, na falta, REQUERIAL_<nome>, o nome de quando o app se
// chamava requerial (agentes subidos antes da renomeação). O legado sai quando não restar nenhum deles.
func (f *Ferramentas) variavel(nome string) string {
	if v := f.ambiente("REX_" + nome); v != "" {
		return v
	}
	return f.ambiente("REQUERIAL_" + nome)
}

// adotar assume a sessão que o lançador abriu para este agente, se houver, e cria a pasta do journal
// dela. Roda uma vez, na primeira ferramenta que precisa da sessão.
func (f *Ferramentas) adotar(ctx context.Context) {
	f.mu.Lock()
	if f.conferida || f.sessao != "" {
		f.mu.Unlock()
		return
	}
	f.conferida = true
	id, token := f.variavel("SESSAO"), f.variavel("TOKEN")
	if id == "" || token == "" {
		f.mu.Unlock()
		return
	}
	f.sessao, f.token, f.adotada = id, token, true
	f.mu.Unlock()
	if f.journals == "" {
		return
	}
	var x api.Sessao
	resp, err := f.cliente.ObterSessao(ctx, id)
	if err := decodificar(resp, err, &x); err != nil || x.Journal != nil {
		return
	}
	if _, err := f.criarJournal(ctx, x, x.Servidor); err != nil {
		log.Printf("criar o journal da sessão %s: %v", id, err)
	}
}

// sessaoAberta devolve a sessão deste processo ou um erro que diz o que fazer.
func (f *Ferramentas) sessaoAberta() (string, error) {
	f.adotar(context.Background())
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sessao == "" {
		return "", errors.New("não há sessão aberta neste chat; chame iniciar_sessao primeiro")
	}
	return f.sessao, nil
}

func (f *Ferramentas) guardar(criada api.SessaoCriada) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessao, f.token = criada.Sessao.Id, criada.Token
}

// decodificar lê a resposta da API: o corpo em destino, ou a mensagem de erro dela.
func decodificar(resp *http.Response, err error, destino any) error {
	if err != nil {
		return fmt.Errorf("chamar a API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e api.Erro
		if json.NewDecoder(resp.Body).Decode(&e) == nil && e.Mensagem != "" {
			return errors.New(e.Mensagem)
		}
		return fmt.Errorf("a API respondeu %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(destino)
}

type Id struct {
	Id string `json:"id" jsonschema:"identificador, como T-0001"`
}

type ConsultaDeTarefas struct {
	Status      []string `json:"status,omitempty" jsonschema:"um ou mais de a_fazer, em_andamento, pausada, bloqueada, em_validacao, concluida"`
	Organizacao string   `json:"organizacao,omitempty" jsonschema:"Cartório, Yes ou Pessoal (ou a da instalação)"`
	Componente  string   `json:"componente,omitempty" jsonschema:"só deste componente, como C-0001"`
	Requisito   string   `json:"requisito,omitempty" jsonschema:"só as que tentam atender este requisito, como R-0001"`
	Limite      int      `json:"limite,omitempty" jsonschema:"quantas no máximo; padrão 20, máximo 50"`
}

type Sessoes struct {
	Sessoes []api.Sessao `json:"sessoes"`
}

type AlteracaoTarefa struct {
	Id string `json:"id" jsonschema:"identificador da tarefa, como T-0001"`
	api.AlteracaoTarefa
}

type NovaSessao struct {
	Modelo   string   `json:"modelo" jsonschema:"provider e modelo, sem o esforço, como 'Claude Opus 5.5'"`
	Esforco  *string  `json:"esforco,omitempty" jsonschema:"esforço de raciocínio (low, medium, high, xhigh, max); padrão: o informado pelo cliente do agente"`
	Chat     *string  `json:"chat,omitempty" jsonschema:"identificador do chat; padrão: o informado pelo cliente do agente"`
	Servidor *string  `json:"servidor,omitempty" jsonschema:"máquina onde a sessão roda; padrão: o nome desta máquina"`
	Journal  *string  `json:"journal,omitempty" jsonschema:"localizador do journal: <maquina>:<caminho>"`
	Tarefas  []string `json:"tarefas,omitempty" jsonschema:"tarefas em que a sessão vai trabalhar; pode ficar vazio e ser vinculado depois. Padrão: a tarefa em REX_TAREFA, se o lançador definiu"`
}

type Repasse struct {
	Modelo   string  `json:"modelo" jsonschema:"provider e modelo do agente novo, como 'Claude Fable 5.1'"`
	Esforco  string  `json:"esforco" jsonschema:"esforço do agente novo (low, medium, high, xhigh, max)"`
	Servidor *string `json:"servidor,omitempty" jsonschema:"padrão: o da sessão anterior"`
}

// agente monta o nome imutável do agente: provider, modelo e esforço, como "Claude Opus 5.5 Medium".
// Sem esforço explícito, usa o que o cliente do agente informou ao processo MCP.
func (f *Ferramentas) agente(modelo string, esforco *string) (string, error) {
	modelo = strings.TrimSpace(modelo)
	if modelo == "" {
		return "", errors.New("informe o modelo, com o provider na frente, como 'Claude Opus 5.5'")
	}
	nivel := ""
	if esforco != nil {
		nivel = strings.TrimSpace(*esforco)
	}
	if nivel == "" {
		nivel = f.ambiente("CLAUDE_EFFORT")
	}
	if nivel == "" {
		return "", errors.New("o esforço não foi informado e o cliente do agente não o forneceu; pergunte ao usuário e passe em 'esforco'")
	}
	return api.NomeDoAgente(modelo, nivel), nil
}

func (f *Ferramentas) chatAtual(informado *string) *string {
	if informado != nil && *informado != "" {
		return informado
	}
	if id := f.ambiente("CLAUDE_CODE_SESSION_ID"); id != "" {
		return &id
	}
	return nil
}

type Vazio struct{}

type FiltroEventos struct {
	Entidade *string `json:"entidade,omitempty" jsonschema:"T-0001, S-0001 etc."`
}

type Eventos struct {
	Eventos []api.Evento `json:"eventos"`
}

func (f *Ferramentas) obterTarefa(ctx context.Context, _ *mcp.CallToolRequest, in Id) (*mcp.CallToolResult, api.Tarefa, error) {
	var out api.Tarefa
	resp, err := f.cliente.ObterTarefa(ctx, in.Id)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) consultarTarefas(ctx context.Context, _ *mcp.CallToolRequest, in ConsultaDeTarefas) (*mcp.CallToolResult, api.ConsultaDeTarefas, error) {
	var out api.ConsultaDeTarefas
	params := api.ConsultarTarefasParams{Status: opcional(strings.Join(in.Status, ",")), Organizacao: opcional(in.Organizacao),
		Componente: opcional(in.Componente), Requisito: opcional(in.Requisito)}
	if in.Limite > 0 {
		params.Limite = &in.Limite
	}
	resp, err := f.cliente.ConsultarTarefas(ctx, &params)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) criarTarefa(ctx context.Context, _ *mcp.CallToolRequest, in api.NovaTarefa) (*mcp.CallToolResult, api.Tarefa, error) {
	var out api.Tarefa
	resp, err := f.cliente.CriarTarefa(ctx, in, f.autorizar)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) alterarTarefa(ctx context.Context, _ *mcp.CallToolRequest, in AlteracaoTarefa) (*mcp.CallToolResult, api.Tarefa, error) {
	var out api.Tarefa
	resp, err := f.cliente.AlterarTarefa(ctx, in.Id, in.AlteracaoTarefa, f.autorizar)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) historicoDaTarefa(ctx context.Context, _ *mcp.CallToolRequest, in Id) (*mcp.CallToolResult, Sessoes, error) {
	var out Sessoes
	resp, err := f.cliente.ListarSessoesDaTarefa(ctx, in.Id)
	return nil, out, decodificar(resp, err, &out.Sessoes)
}

func (f *Ferramentas) iniciarSessao(ctx context.Context, _ *mcp.CallToolRequest, in NovaSessao) (*mcp.CallToolResult, api.Sessao, error) {
	f.adotar(ctx)
	f.mu.Lock()
	adotada, id := f.adotada, f.sessao
	f.mu.Unlock()
	if adotada {
		// O lançador já abriu a sessão deste agente: é ela.
		var x api.Sessao
		resp, err := f.cliente.ObterSessao(ctx, id)
		return nil, x, decodificar(resp, err, &x)
	}
	if atual, err := f.sessaoAberta(); err == nil {
		return nil, api.Sessao{}, fmt.Errorf("este chat já tem a sessão %s aberta; use vincular_tarefa ou encerre-a antes", atual)
	}
	agente, err := f.agente(in.Modelo, in.Esforco)
	if err != nil {
		return nil, api.Sessao{}, err
	}
	servidor, _ := os.Hostname()
	if in.Servidor != nil && *in.Servidor != "" {
		servidor = *in.Servidor
	}
	tarefas := in.Tarefas
	if len(tarefas) == 0 {
		if t := f.variavel("TAREFA"); t != "" {
			tarefas = []string{t}
		}
	}
	var criada api.SessaoCriada
	resp, err := f.cliente.CriarSessao(ctx, api.NovaSessao{
		Agente: agente, TipoAgente: api.Ia, Chat: f.chatAtual(in.Chat), Servidor: servidor, Journal: in.Journal, Tarefas: tarefas,
	})
	if err := decodificar(resp, err, &criada); err != nil {
		return nil, api.Sessao{}, err
	}
	f.guardar(criada)
	if in.Journal == nil && f.journals != "" {
		sessao, err := f.criarJournal(ctx, criada.Sessao, servidor)
		if err != nil {
			return nil, criada.Sessao, fmt.Errorf("sessão %s aberta, mas a pasta do journal falhou: %w", criada.Sessao.Id, err)
		}
		return nil, sessao, nil
	}
	return nil, criada.Sessao, nil
}

// criarJournal cria <journals>/<S-0001>/journal.md e registra o localizador na sessão.
func (f *Ferramentas) criarJournal(ctx context.Context, x api.Sessao, servidor string) (api.Sessao, error) {
	pasta := filepath.Join(f.journals, x.Id)
	if err := os.MkdirAll(pasta, 0o755); err != nil {
		return x, err
	}
	arquivo := filepath.Join(pasta, "journal.md")
	if _, err := os.Stat(arquivo); errors.Is(err, os.ErrNotExist) {
		cabecalho := fmt.Sprintf("# Sessão %s\n\n- Tarefas: %s\n- Agente: %s\n- Máquina: %s\n- Início: %s\n\n## Journal\n\n",
			x.Id, strings.Join(x.Tarefas, ", "), x.Agente, servidor, x.IniciadaEm.Format(time.RFC3339))
		if err := os.WriteFile(arquivo, []byte(cabecalho), 0o644); err != nil {
			return x, err
		}
	}
	journal := servidor + ":" + arquivo
	var alterada api.Sessao
	resp, err := f.cliente.AlterarSessao(ctx, x.Id, api.AlteracaoSessao{Journal: &journal}, f.autorizar)
	return alterada, decodificar(resp, err, &alterada)
}

func (f *Ferramentas) sessaoAtual(ctx context.Context, _ *mcp.CallToolRequest, _ Vazio) (*mcp.CallToolResult, api.Sessao, error) {
	id, err := f.sessaoAberta()
	if err != nil {
		return nil, api.Sessao{}, err
	}
	var out api.Sessao
	resp, err := f.cliente.ObterSessao(ctx, id)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) vincularTarefa(ctx context.Context, _ *mcp.CallToolRequest, in api.Vinculo) (*mcp.CallToolResult, api.Sessao, error) {
	id, err := f.sessaoAberta()
	if err != nil {
		return nil, api.Sessao{}, err
	}
	var out api.Sessao
	resp, err := f.cliente.VincularTarefa(ctx, id, in, f.autorizar)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) atualizarJournal(ctx context.Context, _ *mcp.CallToolRequest, in JournalDaSessao) (*mcp.CallToolResult, api.Sessao, error) {
	return f.alterarSessao(ctx, api.AlteracaoSessao{Journal: in.Journal, Artefatos: in.Artefatos})
}

func (f *Ferramentas) renomearSessao(ctx context.Context, _ *mcp.CallToolRequest, in NomeDaSessao) (*mcp.CallToolResult, api.Sessao, error) {
	return f.alterarSessao(ctx, api.AlteracaoSessao{Nome: &in.Nome})
}

func (f *Ferramentas) alterarSessao(ctx context.Context, in api.AlteracaoSessao) (*mcp.CallToolResult, api.Sessao, error) {
	id, err := f.sessaoAberta()
	if err != nil {
		return nil, api.Sessao{}, err
	}
	var out api.Sessao
	resp, err := f.cliente.AlterarSessao(ctx, id, in, f.autorizar)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) encerrarSessao(ctx context.Context, _ *mcp.CallToolRequest, in api.Encerramento) (*mcp.CallToolResult, api.Sessao, error) {
	id, err := f.sessaoAberta()
	if err != nil {
		return nil, api.Sessao{}, err
	}
	var out api.Sessao
	resp, err := f.cliente.EncerrarSessao(ctx, id, in, f.autorizar)
	if err := decodificar(resp, err, &out); err != nil {
		return nil, out, err
	}
	f.guardar(api.SessaoCriada{})
	return nil, out, nil
}

func (f *Ferramentas) repassarSessao(ctx context.Context, _ *mcp.CallToolRequest, in Repasse) (*mcp.CallToolResult, api.Sessao, error) {
	id, err := f.sessaoAberta()
	if err != nil {
		return nil, api.Sessao{}, err
	}
	// O esforço é obrigatório aqui: o do ambiente é o do início do chat e pode estar desatualizado.
	if strings.TrimSpace(in.Esforco) == "" {
		return nil, api.Sessao{}, errors.New("informe o esforço do agente novo em 'esforco'")
	}
	agente, err := f.agente(in.Modelo, &in.Esforco)
	if err != nil {
		return nil, api.Sessao{}, err
	}
	var criada api.SessaoCriada
	resp, err := f.cliente.RepassarSessao(ctx, id, api.Repasse{Agente: agente, TipoAgente: api.Ia, Servidor: in.Servidor}, f.autorizar)
	if err := decodificar(resp, err, &criada); err != nil {
		return nil, api.Sessao{}, err
	}
	f.guardar(criada)
	return nil, criada.Sessao, nil
}

func (f *Ferramentas) listarEventos(ctx context.Context, _ *mcp.CallToolRequest, in FiltroEventos) (*mcp.CallToolResult, Eventos, error) {
	var out Eventos
	resp, err := f.cliente.ListarEventos(ctx, &api.ListarEventosParams{Entidade: in.Entidade})
	return nil, out, decodificar(resp, err, &out.Eventos)
}

type AlteracaoRequisito struct {
	Id string `json:"id" jsonschema:"identificador do requisito, como R-0001"`
	api.AlteracaoRequisito
}

type Derivacao struct {
	Id     string `json:"id" jsonschema:"requisito que deriva, como R-0002"`
	Pai    string `json:"pai" jsonschema:"requisito do qual ele deriva, como R-0001"`
	Pontos *int   `json:"pontos,omitempty" jsonschema:"pontos de importância sob este pai (mínimo 1)"`
}

type PontosDoRequisito struct {
	Id     string `json:"id" jsonschema:"requisito, como R-0002"`
	Pai    string `json:"pai" jsonschema:"pai sob o qual valem os pontos, como R-0001"`
	Pontos *int   `json:"pontos,omitempty" jsonschema:"pontos de importância (mínimo 1); ausente volta a não triado"`
}

type VinculoRequisito struct {
	Tarefa    string `json:"tarefa" jsonschema:"tarefa, como T-0001"`
	Requisito string `json:"requisito" jsonschema:"requisito que a tarefa tenta atender, como R-0001"`
}

type NovaExecucao struct {
	Requisito string `json:"requisito" jsonschema:"requisito verificado, como R-0001"`
	api.NovaExecucao
}

type Componentes struct {
	Componentes []api.Componente `json:"componentes"`
}

type VinculoComponente struct {
	Componente string `json:"componente" jsonschema:"componente, como C-0001"`
	Requisito  string `json:"requisito,omitempty" jsonschema:"requisito, como R-0001"`
	Tarefa     string `json:"tarefa,omitempty" jsonschema:"tarefa, como T-0001"`
}

type BuscaDeRequisitos struct {
	Texto       string `json:"texto" jsonschema:"palavras a buscar"`
	Organizacao string `json:"organizacao,omitempty" jsonschema:"só desta organização"`
	Componente  string `json:"componente,omitempty" jsonschema:"só deste componente, como C-0001"`
	Obsoletos   bool   `json:"incluir_obsoletos,omitempty" jsonschema:"incluir os requisitos obsoletos (por padrão ficam de fora)"`
}

type Requisitos struct {
	Requisitos []api.ResumoDeRequisito `json:"requisitos"`
}

type FiltroDaArvore struct {
	Organizacao string `json:"organizacao,omitempty" jsonschema:"organização (o zero dela); vazio é a visão geral"`
	Componente  string `json:"componente,omitempty" jsonschema:"só os requisitos deste componente e os ancestrais"`
	Obsoletos   bool   `json:"incluir_obsoletos,omitempty" jsonschema:"incluir os requisitos obsoletos, marcados [obsoleto] (por padrão ficam de fora)"`
}

type Obsolescencia struct {
	Id       string `json:"id" jsonschema:"requisito, como R-0001"`
	Motivo   string `json:"motivo,omitempty" jsonschema:"por que deixou de valer (obrigatório ao marcar)"`
	Reativar bool   `json:"reativar,omitempty" jsonschema:"desfaz a obsolescência, em vez de marcar"`
}

func seMarcado(b bool) *bool {
	if !b {
		return nil
	}
	return &b
}

func opcional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type Lancamento struct {
	Tarefa    string `json:"tarefa" jsonschema:"tarefa, como T-0001"`
	Instrucao string `json:"instrucao,omitempty" jsonschema:"instrução adicional para o agente; o resto ele lê da tarefa e das sessões anteriores"`
	Modelo    string `json:"modelo,omitempty" jsonschema:"um dos modelos de opcoes_do_lancador; padrão: o configurado"`
	Esforco   string `json:"esforco,omitempty" jsonschema:"um dos esforços de opcoes_do_lancador; padrão: o configurado"`
	Nome      string `json:"nome,omitempty" jsonschema:"nome curto do agente (tmux e remote-control) que diz o assunto do trabalho, como dados-privado-joao; informe sempre que souber do que se trata; sem apelido de repositório; padrão: derivado do título"`
}

type JournalDaSessao struct {
	Journal   *string   `json:"journal,omitempty" jsonschema:"localizador do journal"`
	Artefatos *[]string `json:"artefatos,omitempty" jsonschema:"localizadores dos artefatos"`
}

type NomeDaSessao struct {
	Nome string `json:"nome" jsonschema:"nome curto que diz em poucas palavras o assunto do trabalho, como dados-privado-joao; sem verbo genérico (retomar, revisar) e sem apelido de repositório"`
}

type AgenteAtivo struct {
	Sessao     string   `json:"sessao"`
	Nome       string   `json:"nome,omitempty"`
	Agente     string   `json:"agente"`
	Tarefas    []string `json:"tarefas"`
	IniciadaEm string   `json:"iniciada_em"`
}

type AgentesAtivos struct {
	Agentes []AgenteAtivo `json:"agentes"`
}

type ResumoDeTemplate struct {
	Id                string  `json:"id"`
	Titulo            string  `json:"titulo"`
	Organizacao       string  `json:"organizacao"`
	PeriodicidadeDias *int    `json:"periodicidade_dias,omitempty"`
	Devido            bool    `json:"devido"`
	InstanciaAberta   *string `json:"instancia_aberta,omitempty"`
}

type Templates struct {
	Templates []ResumoDeTemplate `json:"templates"`
}

type Instancia struct {
	Template string `json:"template" jsonschema:"pré-moldada, como TP-0001"`
	Contexto string `json:"contexto,omitempty" jsonschema:"informações desta vez (link, quem pediu, o que mudou)"`
}

type Origens struct {
	Origens []api.Origem `json:"origens"`
}

func (f *Ferramentas) buscarRequisitos(ctx context.Context, _ *mcp.CallToolRequest, in BuscaDeRequisitos) (*mcp.CallToolResult, Requisitos, error) {
	var out Requisitos
	resp, err := f.cliente.BuscarRequisitos(ctx, &api.BuscarRequisitosParams{Q: in.Texto, Organizacao: opcional(in.Organizacao), Componente: opcional(in.Componente),
		IncluirObsoletos: seMarcado(in.Obsoletos)})
	return nil, out, decodificar(resp, err, &out.Requisitos)
}

func (f *Ferramentas) arvoreDeRequisitos(ctx context.Context, _ *mcp.CallToolRequest, in FiltroDaArvore) (*mcp.CallToolResult, api.ArvoreDeRequisitos, error) {
	var out api.ArvoreDeRequisitos
	resp, err := f.cliente.ArvoreDeRequisitos(ctx, &api.ArvoreDeRequisitosParams{Organizacao: opcional(in.Organizacao), Componente: opcional(in.Componente),
		IncluirObsoletos: seMarcado(in.Obsoletos)})
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) obterRequisito(ctx context.Context, _ *mcp.CallToolRequest, in Id) (*mcp.CallToolResult, api.Requisito, error) {
	var out api.Requisito
	resp, err := f.cliente.ObterRequisito(ctx, in.Id)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) criarRequisito(ctx context.Context, _ *mcp.CallToolRequest, in api.NovoRequisito) (*mcp.CallToolResult, api.Requisito, error) {
	var out api.Requisito
	resp, err := f.cliente.CriarRequisito(ctx, in, f.autorizar)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) alterarRequisito(ctx context.Context, _ *mcp.CallToolRequest, in AlteracaoRequisito) (*mcp.CallToolResult, api.Requisito, error) {
	var out api.Requisito
	resp, err := f.cliente.AlterarRequisito(ctx, in.Id, in.AlteracaoRequisito, f.autorizar)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) derivarRequisito(ctx context.Context, _ *mcp.CallToolRequest, in Derivacao) (*mcp.CallToolResult, api.Requisito, error) {
	var out api.Requisito
	resp, err := f.cliente.DerivarRequisito(ctx, in.Id, api.Derivacao{Pai: in.Pai, Pontos: in.Pontos}, f.autorizar)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) definirPontos(ctx context.Context, _ *mcp.CallToolRequest, in PontosDoRequisito) (*mcp.CallToolResult, api.Requisito, error) {
	var out api.Requisito
	resp, err := f.cliente.DefinirPontos(ctx, in.Id, in.Pai, api.Pontos{Pontos: in.Pontos}, f.autorizar)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) marcarObsoleto(ctx context.Context, _ *mcp.CallToolRequest, in Obsolescencia) (*mcp.CallToolResult, api.Requisito, error) {
	var out api.Requisito
	var resp *http.Response
	var err error
	if in.Reativar {
		resp, err = f.cliente.ReativarRequisito(ctx, in.Id, f.autorizar)
	} else {
		resp, err = f.cliente.MarcarObsoleto(ctx, in.Id, api.Obsolescencia{Motivo: in.Motivo}, f.autorizar)
	}
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) vincularRequisito(ctx context.Context, _ *mcp.CallToolRequest, in VinculoRequisito) (*mcp.CallToolResult, api.Tarefa, error) {
	var out api.Tarefa
	resp, err := f.cliente.VincularRequisito(ctx, in.Tarefa, api.VinculoRequisito{Requisito: in.Requisito}, f.autorizar)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) registrarExecucao(ctx context.Context, _ *mcp.CallToolRequest, in NovaExecucao) (*mcp.CallToolResult, api.Execucao, error) {
	var out api.Execucao
	resp, err := f.cliente.RegistrarExecucao(ctx, in.Requisito, in.NovaExecucao, f.autorizar)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) listarComponentes(ctx context.Context, _ *mcp.CallToolRequest, _ Vazio) (*mcp.CallToolResult, Componentes, error) {
	var out Componentes
	resp, err := f.cliente.ListarComponentes(ctx)
	return nil, out, decodificar(resp, err, &out.Componentes)
}

func (f *Ferramentas) criarComponente(ctx context.Context, _ *mcp.CallToolRequest, in api.NovoComponente) (*mcp.CallToolResult, api.Componente, error) {
	var out api.Componente
	resp, err := f.cliente.CriarComponente(ctx, in, f.autorizar)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) vincularComponente(ctx context.Context, _ *mcp.CallToolRequest, in VinculoComponente) (*mcp.CallToolResult, Vazio, error) {
	corpo := api.VinculoComponente{Componente: in.Componente}
	switch {
	case in.Requisito != "" && in.Tarefa == "":
		resp, err := f.cliente.VincularComponenteARequisito(ctx, in.Requisito, corpo, f.autorizar)
		return nil, Vazio{}, decodificar(resp, err, &api.Requisito{})
	case in.Tarefa != "" && in.Requisito == "":
		resp, err := f.cliente.VincularComponenteATarefa(ctx, in.Tarefa, corpo, f.autorizar)
		return nil, Vazio{}, decodificar(resp, err, &api.Tarefa{})
	}
	return nil, Vazio{}, fmt.Errorf("informe o requisito ou a tarefa (só um dos dois)")
}

func (f *Ferramentas) opcoesDoLancador(ctx context.Context, _ *mcp.CallToolRequest, _ Vazio) (*mcp.CallToolResult, api.OpcoesDeLancamento, error) {
	var out api.OpcoesDeLancamento
	resp, err := f.cliente.OpcoesDoLancador(ctx)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) lancarAgente(ctx context.Context, _ *mcp.CallToolRequest, in Lancamento) (*mcp.CallToolResult, api.Tarefa, error) {
	var out api.Tarefa
	resp, err := f.cliente.LancarAgente(ctx, in.Tarefa, api.PedidoDeLancamento{Instrucao: opcional(in.Instrucao),
		Modelo: opcional(in.Modelo), Esforco: opcional(in.Esforco), Nome: opcional(in.Nome)}, f.autorizar)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) agentesAtivos(ctx context.Context, _ *mcp.CallToolRequest, _ Vazio) (*mcp.CallToolResult, AgentesAtivos, error) {
	ativa := api.Ativa
	var sessoes []api.Sessao
	resp, err := f.cliente.ListarSessoes(ctx, &api.ListarSessoesParams{Status: &ativa})
	if err := decodificar(resp, err, &sessoes); err != nil {
		return nil, AgentesAtivos{}, err
	}
	out := AgentesAtivos{Agentes: []AgenteAtivo{}}
	for _, x := range sessoes {
		nome := ""
		if x.Nome != nil {
			nome = *x.Nome
		}
		out.Agentes = append(out.Agentes, AgenteAtivo{x.Id, nome, x.Agente, x.Tarefas, x.IniciadaEm.Format("2006-01-02 15:04")})
	}
	return nil, out, nil
}

func (f *Ferramentas) listarTemplates(ctx context.Context, _ *mcp.CallToolRequest, _ Vazio) (*mcp.CallToolResult, Templates, error) {
	var todos []api.Template
	resp, err := f.cliente.ListarTemplates(ctx)
	if err := decodificar(resp, err, &todos); err != nil {
		return nil, Templates{}, err
	}
	out := Templates{Templates: []ResumoDeTemplate{}}
	for _, t := range todos {
		out.Templates = append(out.Templates, ResumoDeTemplate{t.Id, t.Titulo, t.Organizacao, t.PeriodicidadeDias, t.Devido, t.InstanciaAberta})
	}
	return nil, out, nil
}

func (f *Ferramentas) instanciarTemplate(ctx context.Context, _ *mcp.CallToolRequest, in Instancia) (*mcp.CallToolResult, api.Tarefa, error) {
	var out api.Tarefa
	resp, err := f.cliente.InstanciarTemplate(ctx, in.Template, api.PedidoDeInstancia{Contexto: opcional(in.Contexto)}, f.autorizar)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) listarOrigens(ctx context.Context, _ *mcp.CallToolRequest, _ Vazio) (*mcp.CallToolResult, Origens, error) {
	var out Origens
	resp, err := f.cliente.ListarOrigens(ctx)
	return nil, out, decodificar(resp, err, &out.Origens)
}

func (f *Ferramentas) criarOrigem(ctx context.Context, _ *mcp.CallToolRequest, in api.NovaOrigem) (*mcp.CallToolResult, api.Origem, error) {
	var out api.Origem
	resp, err := f.cliente.CriarOrigem(ctx, in, f.autorizar)
	return nil, out, decodificar(resp, err, &out)
}

type NovoLembrete struct {
	Texto       string `json:"texto" jsonschema:"o que lembrar, como o Rafael vai ler"`
	Curto       string `json:"curto,omitempty" jsonschema:"palavra-chave do lembrete, até 10 caracteres, para a ilha do iPhone (ballet, dentista, Vivo); escolha a que identifica o compromisso"`
	Quando      string `json:"quando" jsonschema:"hora marcada em RFC 3339 com fuso, como 2026-10-21T09:00:00-03:00; fora de hora, uma hora dentro da janela: em turno, é ela que escolhe o turno (manhã 10:00, almoço 12:30, tarde 15:00, noite 20:00); em dia, semana ou mês, qualquer hora de um dia dentro dele"`
	Precisao    string `json:"precisao" jsonschema:"hora (o Rafael disse data e hora explícitas), turno (manhã, almoço, tarde ou noite), dia (um dia, sem hora), semana (uma semana, sem dia) ou mes (um mês, sem dia); infira da mensagem dele"`
	Organizacao string `json:"organizacao" jsonschema:"Cartório, Yes ou Pessoal (ou a da instalação)"`
	Tarefa      string `json:"tarefa,omitempty" jsonschema:"tarefa ligada, como T-0001"`
	Requisito   string `json:"requisito,omitempty" jsonschema:"requisito ligado, como R-0001"`
}

type ResumoDeLembrete struct {
	Id       string `json:"id"`
	Texto    string `json:"texto"`
	Curto    string `json:"curto,omitempty"`
	Quando   string `json:"quando"`
	Precisao string `json:"precisao"`
	Fim      string `json:"fim,omitempty"`
	Situacao string `json:"situacao" jsonschema:"vencido (a janela acabou e ainda pede atenção), na_janela (a janela está aberta) ou futuro"`
	Status   string `json:"status"`
}

type Lembretes struct {
	// Agora é a hora do rex, com fuso: o conector do Claude não sabe a hora para calcular o quando.
	Agora     string             `json:"agora"`
	Lembretes []ResumoDeLembrete `json:"lembretes"`
}

type FiltroDeLembretes struct {
	Horas int `json:"horas,omitempty" jsonschema:"janela à frente, em horas; padrão 24"`
}

type AdiamentoDeLembrete struct {
	Id      string `json:"id" jsonschema:"lembrete, como L-0001"`
	Minutos int    `json:"minutos,omitempty" jsonschema:"daqui a quantos minutos"`
	Ate     string `json:"ate,omitempty" jsonschema:"ou a nova hora, em RFC 3339 com fuso"`
	// MesmoNaJanela libera o adiamento de um lembrete de semana ou mês que ainda vale.
	MesmoNaJanela bool `json:"mesmo_na_janela,omitempty" jsonschema:"true só quando o Rafael pediu para adiar este lembrete de semana ou mês pelo nome, com a janela ainda aberta"`
}

type AlteracaoDeLembrete struct {
	Id          string  `json:"id" jsonschema:"lembrete, como L-0001"`
	Texto       string  `json:"texto,omitempty" jsonschema:"novo texto, como o Rafael vai ler"`
	Curto       *string `json:"curto,omitempty" jsonschema:"nova palavra-chave, até 10 caracteres; vazio remove"`
	Quando      string  `json:"quando,omitempty" jsonschema:"nova hora marcada, em RFC 3339 com fuso"`
	Precisao    string  `json:"precisao,omitempty" jsonschema:"nova precisão: hora, turno, dia, semana ou mes"`
	Organizacao string  `json:"organizacao,omitempty" jsonschema:"nova organização"`
	Tarefa      *string `json:"tarefa,omitempty" jsonschema:"tarefa ligada, como T-0001; vazio remove a ligação"`
	Requisito   *string `json:"requisito,omitempty" jsonschema:"requisito ligado, como R-0001; vazio remove a ligação"`
}

type Espera struct {
	Mensagem string `json:"mensagem" jsonschema:"o que você precisa do Rafael, em uma linha"`
}

// Situações de um lembrete em relação à própria janela, na ordem em que proximos_lembretes as lista.
const (
	vencido  = "vencido"
	naJanela = "na_janela"
	futuro   = "futuro"
)

// situacao compara a janela do lembrete com agora. Na precisão hora a janela é o instante, então
// o que já passou está vencido; nas outras, quando é o início e fim o fim da janela.
func situacao(x api.Lembrete, agora time.Time) string {
	switch {
	case x.Quando.After(agora):
		return futuro
	case x.Fim != nil && x.Fim.After(agora):
		return naJanela
	}
	return vencido
}

func resumirLembrete(x api.Lembrete) ResumoDeLembrete {
	r := ResumoDeLembrete{Id: x.Id, Texto: x.Texto, Quando: x.Quando.Format(time.RFC3339), Precisao: string(x.Precisao),
		Situacao: situacao(x, time.Now()), Status: string(x.Status)}
	if x.Curto != nil {
		r.Curto = *x.Curto
	}
	if x.Fim != nil {
		r.Fim = x.Fim.In(x.Quando.Location()).Format(time.RFC3339)
	}
	return r
}

func lerHora(campo, valor string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, valor)
	if err != nil {
		return t, fmt.Errorf("%s: use RFC 3339 com fuso, como 2026-10-21T09:00:00-03:00 (%v)", campo, err)
	}
	return t, nil
}

func (f *Ferramentas) criarLembrete(ctx context.Context, _ *mcp.CallToolRequest, in NovoLembrete) (*mcp.CallToolResult, ResumoDeLembrete, error) {
	quando, err := lerHora("quando", in.Quando)
	if err != nil {
		return nil, ResumoDeLembrete{}, err
	}
	if in.Precisao == "" {
		return nil, ResumoDeLembrete{}, fmt.Errorf("precisao: informe hora, turno, dia, semana ou mes, conforme o que o Rafael disse")
	}
	precisao := api.PrecisaoLembrete(in.Precisao)
	var x api.Lembrete
	resp, err := f.cliente.CriarLembrete(ctx, api.NovoLembrete{
		Texto: in.Texto, Curto: opcional(in.Curto), Quando: quando, Precisao: &precisao, Organizacao: in.Organizacao, Tarefa: opcional(in.Tarefa), Requisito: opcional(in.Requisito),
	}, f.autorizar)
	if err := decodificar(resp, err, &x); err != nil {
		return nil, ResumoDeLembrete{}, err
	}
	return nil, resumirLembrete(x), nil
}

func (f *Ferramentas) proximosLembretes(ctx context.Context, _ *mcp.CallToolRequest, in FiltroDeLembretes) (*mcp.CallToolResult, Lembretes, error) {
	horas := in.Horas
	if horas <= 0 {
		horas = 24
	}
	horizonte := horas * 60
	var p api.Pendencias
	resp, err := f.cliente.ListarPendencias(ctx, &api.ListarPendenciasParams{Horizonte: &horizonte})
	if err := decodificar(resp, err, &p); err != nil {
		return nil, Lembretes{}, err
	}
	out := Lembretes{Agora: time.Now().Format(time.RFC3339), Lembretes: []ResumoDeLembrete{}}
	for _, x := range p.Lembretes {
		out.Lembretes = append(out.Lembretes, resumirLembrete(x))
	}
	// Separados por situação para quem lê a lista não tomar o lembrete da semana por atrasado.
	ordem := map[string]int{vencido: 0, naJanela: 1, futuro: 2}
	sort.SliceStable(out.Lembretes, func(i, j int) bool { return ordem[out.Lembretes[i].Situacao] < ordem[out.Lembretes[j].Situacao] })
	return nil, out, nil
}

func (f *Ferramentas) adiarLembrete(ctx context.Context, _ *mcp.CallToolRequest, in AdiamentoDeLembrete) (*mcp.CallToolResult, ResumoDeLembrete, error) {
	var a api.Adiamento
	if in.Minutos > 0 {
		a.Minutos = &in.Minutos
	}
	if in.Ate != "" {
		ate, err := lerHora("ate", in.Ate)
		if err != nil {
			return nil, ResumoDeLembrete{}, err
		}
		a.Ate = &ate
	}
	if !in.MesmoNaJanela {
		var atual api.Lembrete
		resp, err := f.cliente.ObterLembrete(ctx, in.Id)
		if err := decodificar(resp, err, &atual); err != nil {
			return nil, ResumoDeLembrete{}, err
		}
		if (atual.Precisao == api.PrecisaoSemana || atual.Precisao == api.PrecisaoMes) && situacao(atual, time.Now()) == naJanela {
			return nil, ResumoDeLembrete{}, fmt.Errorf("%s é de %s e ainda vale até %s: não está atrasado; não adiei. "+
				"Só adie com mesmo_na_janela se o Rafael pediu este lembrete pelo nome", atual.Id, atual.Precisao, atual.Fim.In(atual.Quando.Location()).Format(time.RFC3339))
		}
	}
	var x api.Lembrete
	resp, err := f.cliente.AdiarLembrete(ctx, in.Id, a, f.autorizar)
	if err := decodificar(resp, err, &x); err != nil {
		return nil, ResumoDeLembrete{}, err
	}
	return nil, resumirLembrete(x), nil
}

func (f *Ferramentas) alterarLembrete(ctx context.Context, _ *mcp.CallToolRequest, in AlteracaoDeLembrete) (*mcp.CallToolResult, ResumoDeLembrete, error) {
	a := api.AlteracaoLembrete{Texto: opcional(in.Texto), Curto: in.Curto, Organizacao: opcional(in.Organizacao), Tarefa: in.Tarefa, Requisito: in.Requisito}
	if in.Quando != "" {
		quando, err := lerHora("quando", in.Quando)
		if err != nil {
			return nil, ResumoDeLembrete{}, err
		}
		a.Quando = &quando
	}
	if in.Precisao != "" {
		precisao := api.PrecisaoLembrete(in.Precisao)
		a.Precisao = &precisao
	}
	var x api.Lembrete
	resp, err := f.cliente.AlterarLembrete(ctx, in.Id, a, f.autorizar)
	if err := decodificar(resp, err, &x); err != nil {
		return nil, ResumoDeLembrete{}, err
	}
	return nil, resumirLembrete(x), nil
}

func (f *Ferramentas) confirmarLembrete(ctx context.Context, _ *mcp.CallToolRequest, in Id) (*mcp.CallToolResult, ResumoDeLembrete, error) {
	var x api.Lembrete
	resp, err := f.cliente.ConfirmarLembrete(ctx, in.Id, f.autorizar)
	if err := decodificar(resp, err, &x); err != nil {
		return nil, ResumoDeLembrete{}, err
	}
	return nil, resumirLembrete(x), nil
}

func (f *Ferramentas) cancelarLembrete(ctx context.Context, _ *mcp.CallToolRequest, in Id) (*mcp.CallToolResult, ResumoDeLembrete, error) {
	var x api.Lembrete
	resp, err := f.cliente.CancelarLembrete(ctx, in.Id, f.autorizar)
	if err := decodificar(resp, err, &x); err != nil {
		return nil, ResumoDeLembrete{}, err
	}
	return nil, resumirLembrete(x), nil
}

func (f *Ferramentas) aguardarResposta(ctx context.Context, _ *mcp.CallToolRequest, in Espera) (*mcp.CallToolResult, api.Sessao, error) {
	id, err := f.sessaoAberta()
	if err != nil {
		return nil, api.Sessao{}, err
	}
	var out api.Sessao
	resp, err := f.cliente.AguardarResposta(ctx, id, api.Espera{Mensagem: in.Mensagem}, f.autorizar)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) retomarTrabalho(ctx context.Context, _ *mcp.CallToolRequest, _ Vazio) (*mcp.CallToolResult, api.Sessao, error) {
	id, err := f.sessaoAberta()
	if err != nil {
		return nil, api.Sessao{}, err
	}
	var out api.Sessao
	resp, err := f.cliente.RetomarSessao(ctx, id, f.autorizar)
	return nil, out, decodificar(resp, err, &out)
}

type PanoramaDaVoz struct {
	Sessoes []api.PanoramaDeVoz `json:"sessoes"`
}

type SituacaoDaVoz struct {
	Sessao          string `json:"sessao" jsonschema:"sessão (S-0001) ou tarefa com sessão ativa (T-0001)"`
	EsperarSegundos int    `json:"esperar_segundos,omitempty" jsonschema:"até 20: com o agente trabalhando, espera ele responder algo novo ou parar"`
}

type MensagemDeVoz struct {
	Sessao string `json:"sessao" jsonschema:"sessão (S-0001) ou tarefa com sessão ativa (T-0001)"`
	Texto  string `json:"texto" jsonschema:"o que o Rafael pediu, como ele disse"`
}

type MensagemASessao struct {
	Sessao string `json:"sessao" jsonschema:"sessão de destino (S-0001) ou tarefa com sessão ativa (T-0001); para responder, a sessão que mandou a mensagem"`
	Texto  string `json:"texto" jsonschema:"a mensagem, numa linha"`
}

type TeclaDeVoz struct {
	Sessao string `json:"sessao" jsonschema:"sessão (S-0001) ou tarefa com sessão ativa (T-0001)"`
	Tecla  string `json:"tecla" jsonschema:"1, 2, 3, 4, Enter ou Escape"`
}

func (f *Ferramentas) vozPanorama(ctx context.Context, _ *mcp.CallToolRequest, _ Vazio) (*mcp.CallToolResult, PanoramaDaVoz, error) {
	out := PanoramaDaVoz{Sessoes: []api.PanoramaDeVoz{}}
	resp, err := f.cliente.PanoramaDaVoz(ctx, f.autorizar)
	return nil, out, decodificar(resp, err, &out.Sessoes)
}

func (f *Ferramentas) vozSituacao(ctx context.Context, _ *mcp.CallToolRequest, in SituacaoDaVoz) (*mcp.CallToolResult, api.Voz, error) {
	var out api.Voz
	params := &api.ObterVozParams{Esperar: &in.EsperarSegundos}
	if f.ouvir {
		params.Ouvir = &f.ouvir
	}
	resp, err := f.cliente.ObterVoz(ctx, in.Sessao, params, f.autorizar)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) vozEnviar(ctx context.Context, _ *mcp.CallToolRequest, in MensagemDeVoz) (*mcp.CallToolResult, api.PedidoDeVoz, error) {
	var out api.PedidoDeVoz
	resp, err := f.cliente.EnviarVoz(ctx, in.Sessao, api.MensagemDeVoz{Texto: in.Texto}, f.autorizar)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) enviarASessao(ctx context.Context, _ *mcp.CallToolRequest, in MensagemASessao) (*mcp.CallToolResult, api.MensagemEntreSessoes, error) {
	var out api.MensagemEntreSessoes
	if _, err := f.sessaoAberta(); err != nil {
		return nil, out, err
	}
	resp, err := f.cliente.EnviarASessao(ctx, in.Sessao, api.MensagemDeVoz{Texto: in.Texto}, f.autorizar)
	return nil, out, decodificar(resp, err, &out)
}

func (f *Ferramentas) vozTecla(ctx context.Context, _ *mcp.CallToolRequest, in TeclaDeVoz) (*mcp.CallToolResult, api.Voz, error) {
	var out api.Voz
	resp, err := f.cliente.TeclarVoz(ctx, in.Sessao, api.TeclaDeVoz{Tecla: api.TeclaDeVozTecla(in.Tecla)}, f.autorizar)
	return nil, out, decodificar(resp, err, &out)
}
