// Package lancador sobe e encerra agentes de IA em sessões tmux conforme o status das tarefas.
//
// Quando uma tarefa entra em em_andamento sem sessão ativa, por alguém que não é uma sessão (um
// humano no frontend, ou um agente que só coordena), o lançador abre uma sessão tmux com o agente
// configurado e um prompt que o manda trabalhar na tarefa. Quando a tarefa sai de em_andamento por
// mão de outro, o lançador pede ao agente vivo que escreva o resumo e encerre a sessão. Depois do
// encerramento, o próprio agente fecha o seu tmux (skill rex); um tmux que sobrar é fechado
// quando a tarefa ganhar um agente novo.
//
// A sessão tmux carrega REX_TAREFA no ambiente: é assim que o lançador a reencontra, sem
// guardar estado, e que o agente e o servidor MCP sabem em que tarefa estão.
package lancador

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rpagliuca/rex/api"
	"github.com/rpagliuca/rex/internal/recursos"
	"github.com/rpagliuca/rex/internal/servidor"
)

// VariavelTarefa é a variável de ambiente da sessão tmux com o identificador da tarefa.
const VariavelTarefa = "REX_TAREFA"

// VariavelTarefaLegada é o nome que a variável tinha quando o app se chamava requerial: os agentes
// subidos antes da renomeação ainda a carregam. Sai quando não restar nenhum deles.
const VariavelTarefaLegada = "REQUERIAL_TAREFA"

// Config vem de um arquivo JSON, particular de cada instalação.
type Config struct {
	// Diretorio onde o agente começa a trabalhar (normalmente o clone dos journals).
	Diretorio string `json:"diretorio"`
	// Tmux é o executável do tmux. Padrão: "tmux".
	Tmux string `json:"tmux"`
	// Comando do agente; o prompt entra como último argumento. "{nome}" vira o nome da sessão tmux
	// e "{chat}" o identificador do chat que o lançador escolhe (ex.: --session-id {chat}).
	Comando []string `json:"comando"`
	// PromptIniciar e PromptEncerrar aceitam {tarefa}, {titulo} e {status}.
	PromptIniciar  string `json:"prompt_iniciar"`
	PromptEncerrar string `json:"prompt_encerrar"`
	// Modelos que se pode escolher ao lançar; o primeiro é o padrão. Os argumentos entram no
	// comando logo depois dele.
	Modelos []Modelo `json:"modelos"`
	// Esforcos que se pode escolher; ArgumentosEsforco recebe "{esforco}". Sem esforço escolhido nem
	// EsforcoPadrao, o agente usa o padrão dele.
	Esforcos          []string `json:"esforcos"`
	ArgumentosEsforco []string `json:"argumentos_esforco"`
	EsforcoPadrao     string   `json:"esforco_padrao"`
	// ArgumentosPorTarefa acrescenta argumentos ao comando só do agente de uma tarefa (ex.: um
	// --mcp-config que nenhum outro agente deve carregar). A chave é o identificador da tarefa.
	ArgumentosPorTarefa map[string][]string `json:"argumentos_por_tarefa"`
	// RemoverDoAmbiente lista variáveis que o agente não pode herdar do servidor tmux (por exemplo,
	// as de um agente que abriu o tmux), para que o agente novo defina as suas.
	RemoverDoAmbiente []string `json:"remover_do_ambiente"`
	// Ambiente define variáveis para o agente; os valores aceitam {modelo} e {esforco}. Uma
	// variável cujo valor fica vazio (ex.: sem esforço escolhido) não é definida.
	Ambiente map[string]string `json:"ambiente"`
	// Projetos é onde o Claude Code guarda as conversas (<projetos>/<diretório>/<chat>.jsonl), que o
	// rex voz lê. Padrão: ~/.claude/projects.
	Projetos string `json:"projetos"`
	// Recursos: memória mínima para lançar sem forcar e o nome da sessão do agente navegador.
	Recursos recursos.Config `json:"recursos"`
}

// Modelo é uma opção de modelo do agente.
type Modelo struct {
	Nome       string   `json:"nome"`
	Argumentos []string `json:"argumentos"`
}

const promptIniciarPadrao = "Você vai trabalhar na tarefa {tarefa} do rex ({titulo}). " +
	"A sua sessão de trabalho já foi aberta pelo lançador. Carregue a skill rex e siga o ciclo dela: " +
	"leia a tarefa e as sessões anteriores, confira a sessão com sessao_atual e trabalhe. " +
	"Ao terminar, ou se ficar bloqueado, entregue ao usuário e espere: não encerre a sessão sem pedido claro dele."

const promptEncerrarPadrao = "A tarefa {tarefa} foi movida para {status} por outra pessoa. " +
	"Pare o que está fazendo, atualize o journal e encerre a sessão de trabalho com um resumo para retomar " +
	"(skill rex). Não altere o status da tarefa."

// Sessoes abre e encerra no servidor a sessão de um agente lançado.
type Sessoes interface {
	AbrirSessaoLancada(tarefa, agente, servidor, chat, nome string) (api.SessaoCriada, error)
	EncerrarSessaoLancada(id, resumo string) error
	Sessao(id string) (api.Sessao, error)
}

// Variáveis que levam ao agente (e ao MCP dele) a sessão já aberta pelo lançador.
const (
	VariavelSessao = "REX_SESSAO"
	VariavelToken  = "REX_TOKEN"
)

// Lancador reage às mudanças do servidor.
type Lancador struct {
	cfg     Config
	sessoes Sessoes
	maquina string
	// executar roda o tmux; substituível nos testes.
	executar func(args ...string) (string, error)
	// processosDoChat acha os pids dos processos cujo chat é o dado; substituível nos testes.
	processosDoChat func(chat string) []int
	// paiDe devolve o processo pai; substituível nos testes.
	paiDe func(pid int) int
}

// Carregar lê a configuração do arquivo.
func Carregar(caminho string) (*Lancador, error) {
	conteudo, err := os.ReadFile(caminho)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(conteudo, &cfg); err != nil {
		return nil, fmt.Errorf("configuração do lançador: %w", err)
	}
	return Novo(cfg)
}

// Novo valida a configuração e preenche os padrões.
func Novo(cfg Config) (*Lancador, error) {
	if cfg.Diretorio == "" || len(cfg.Comando) == 0 {
		return nil, errors.New("configuração do lançador: diretorio e comando são obrigatórios")
	}
	if cfg.Tmux == "" {
		cfg.Tmux = "tmux"
	}
	if cfg.PromptIniciar == "" {
		cfg.PromptIniciar = promptIniciarPadrao
	}
	if cfg.PromptEncerrar == "" {
		cfg.PromptEncerrar = promptEncerrarPadrao
	}
	if cfg.Projetos == "" {
		if casa, err := os.UserHomeDir(); err == nil {
			cfg.Projetos = filepath.Join(casa, ".claude", "projects")
		}
	}
	maquina, _ := os.Hostname()
	l := &Lancador{cfg: cfg, maquina: maquina, processosDoChat: processosDoChat, paiDe: paiDe}
	l.executar = func(args ...string) (string, error) {
		saida, err := exec.Command(l.cfg.Tmux, args...).CombinedOutput()
		return strings.TrimSpace(string(saida)), err
	}
	return l, nil
}

// Conectar registra o lançador nos avisos e nos endpoints de lançamento do servidor.
func (l *Lancador) Conectar(s *servidor.Servidor) {
	s.AoMudarStatus(l.MudouStatus)
	s.AoRenomearSessao(l.Renomeou)
	s.ComLancador(l)
	s.ComVoz(l)
	s.ComMedidor(recursos.Novo(l.cfg.Recursos))
	l.sessoes = s
}

// Opcoes lista os modelos e esforços configurados.
func (l *Lancador) Opcoes() api.OpcoesDeLancamento {
	o := api.OpcoesDeLancamento{Modelos: []string{}, Esforcos: l.cfg.Esforcos}
	if o.Esforcos == nil {
		o.Esforcos = []string{}
	}
	for _, m := range l.cfg.Modelos {
		o.Modelos = append(o.Modelos, m.Nome)
	}
	if len(o.Modelos) > 0 {
		o.ModeloPadrao = o.Modelos[0]
	}
	if l.cfg.EsforcoPadrao != "" {
		o.EsforcoPadrao = &l.cfg.EsforcoPadrao
	}
	return o
}

// Validar confere o pedido contra a configuração e preenche os padrões.
func (l *Lancador) Validar(p *api.PedidoDeLancamento) error {
	if p.Modelo == nil || *p.Modelo == "" {
		if len(l.cfg.Modelos) > 0 {
			p.Modelo = &l.cfg.Modelos[0].Nome
		}
	} else if l.modelo(*p.Modelo) == nil {
		return fmt.Errorf("modelo desconhecido: %q", *p.Modelo)
	}
	if p.Esforco == nil || *p.Esforco == "" {
		if l.cfg.EsforcoPadrao != "" {
			p.Esforco = &l.cfg.EsforcoPadrao
		} else {
			p.Esforco = nil
		}
	} else if !contem(l.cfg.Esforcos, *p.Esforco) {
		return fmt.Errorf("esforço desconhecido: %q", *p.Esforco)
	}
	if p.Nome != nil && strings.TrimSpace(*p.Nome) == "" {
		p.Nome = nil
	} else if p.Nome != nil {
		nome := api.NomeCurto(*p.Nome)
		if nome == "" {
			return fmt.Errorf("o nome precisa ter ao menos uma letra ou número: %q", *p.Nome)
		}
		p.Nome = &nome
	}
	return nil
}

func (l *Lancador) modelo(nome string) *Modelo {
	for i := range l.cfg.Modelos {
		if l.cfg.Modelos[i].Nome == nome {
			return &l.cfg.Modelos[i]
		}
	}
	return nil
}

func contem(lista []string, valor string) bool {
	for _, v := range lista {
		if v == valor {
			return true
		}
	}
	return false
}

// MudouStatus decide se sobe ou avisa um agente.
func (l *Lancador) MudouStatus(m servidor.MudancaDeStatus) {
	t := m.Tarefa
	autorESessao := strings.HasPrefix(m.Autor, "S-")
	switch {
	case m.Lancamento != nil && t.SessaoAtiva == nil:
		// Pedido explícito, com opções: vale mesmo se a tarefa já estava em andamento.
		l.registrar(l.iniciar(t, *m.Lancamento))
	case t.Status == api.EmAndamento && m.Anterior != api.EmAndamento && t.SessaoAtiva == nil && !autorESessao:
		// Uma sessão que põe a tarefa em andamento vai trabalhar nela ela mesma.
		var pedido api.PedidoDeLancamento
		l.registrar(l.Validar(&pedido))
		l.registrar(l.iniciar(t, pedido))
	case m.Anterior == api.EmAndamento && t.Status != api.EmAndamento && t.SessaoAtiva != nil && m.Autor != *t.SessaoAtiva:
		l.registrar(l.avisar(t))
	}
}

func (l *Lancador) iniciar(t api.Tarefa, pedido api.PedidoDeLancamento) error {
	// Um tmux antigo desta tarefa, sem sessão ativa, é de um agente que já terminou.
	if antigo, err := l.encontrar(t.Id); err != nil {
		return err
	} else if antigo != "" {
		if _, err := l.executar("kill-session", "-t", "="+antigo); err != nil {
			return fmt.Errorf("fechar o tmux antigo %s: %w", antigo, err)
		}
	}
	base := nomeCurto(t.Titulo)
	if pedido.Nome != nil {
		base = *pedido.Nome
	}
	nome, err := l.nomeLivre(base)
	if err != nil {
		return err
	}
	// A sessão de trabalho nasce aqui: quem lança já sabe a tarefa, o agente e o chat.
	chat, err := novoUUID()
	if err != nil {
		return err
	}
	var sessao *api.SessaoCriada
	if l.sessoes != nil && pedido.Modelo != nil {
		esforco := ""
		if pedido.Esforco != nil {
			esforco = *pedido.Esforco
		}
		criada, err := l.sessoes.AbrirSessaoLancada(t.Id, api.NomeDoAgente(*pedido.Modelo, esforco), l.maquina, chat, nome)
		if err != nil {
			return fmt.Errorf("abrir a sessão da tarefa %s: %w", t.Id, err)
		}
		sessao = &criada
	}
	args := []string{"new-session", "-d", "-s", nome, "-c", l.cfg.Diretorio, "-e", VariavelTarefa + "=" + t.Id}
	if sessao != nil {
		args = append(args, "-e", VariavelSessao+"="+sessao.Sessao.Id, "-e", VariavelToken+"="+sessao.Token)
	}
	definir := l.ambiente(pedido)
	if len(l.cfg.RemoverDoAmbiente) > 0 || len(definir) > 0 {
		args = append(args, "env")
		for _, v := range l.cfg.RemoverDoAmbiente {
			args = append(args, "-u", v)
		}
		args = append(args, definir...)
	}
	for _, parte := range l.cfg.Comando {
		args = append(args, strings.NewReplacer("{nome}", nome, "{chat}", chat).Replace(parte))
	}
	if pedido.Modelo != nil {
		if m := l.modelo(*pedido.Modelo); m != nil {
			args = append(args, m.Argumentos...)
		}
	}
	if pedido.Esforco != nil {
		for _, parte := range l.cfg.ArgumentosEsforco {
			args = append(args, strings.ReplaceAll(parte, "{esforco}", *pedido.Esforco))
		}
	}
	args = append(args, l.cfg.ArgumentosPorTarefa[t.Id]...)
	prompt := preencher(l.cfg.PromptIniciar, t)
	if pedido.Instrucao != nil && strings.TrimSpace(*pedido.Instrucao) != "" {
		prompt += "\n\nInstrução adicional de quem reabriu a tarefa (o resto do contexto está na tarefa e nas " +
			"sessões anteriores):\n" + strings.TrimSpace(*pedido.Instrucao)
	}
	args = append(args, prompt)
	if saida, err := l.executar(args...); err != nil {
		if sessao != nil {
			l.registrar(l.sessoes.EncerrarSessaoLancada(sessao.Sessao.Id, "O lançador não conseguiu subir o agente: "+saida))
		}
		return fmt.Errorf("subir o agente da tarefa %s: %v: %s", t.Id, err, saida)
	}
	log.Printf("lançador: agente da tarefa %s subiu no tmux %s", t.Id, nome)
	return nil
}

// Renomeou aplica o nome curto novo de uma sessão ao agente vivo: renomeia o tmux dele e manda
// /rename ao Claude Code, que troca o nome também no remote-control. O agente pode estar no meio de
// um turno: o /rename é aplicado na hora, sem interromper.
func (l *Lancador) Renomeou(x api.Sessao) {
	if x.Nome == nil || *x.Nome == "" {
		return
	}
	atual := l.encontrarPeloChat(x.Id)
	for _, t := range x.Tarefas {
		if atual != "" {
			break
		}
		var err error
		if atual, err = l.encontrar(t); err != nil {
			l.registrar(err)
			return
		}
	}
	if atual == "" {
		log.Printf("lançador: sessão %s renomeada para %s, mas não achei o tmux do agente", x.Id, *x.Nome)
		return
	}
	novo := atual
	if atual != *x.Nome {
		var err error
		if novo, err = l.nomeLivre(*x.Nome); err != nil {
			l.registrar(err)
			return
		}
		if _, err := l.executar("rename-session", "-t", "="+atual, novo); err != nil {
			l.registrar(fmt.Errorf("renomear o tmux %s: %w", atual, err))
			return
		}
	}
	if _, err := l.executar("send-keys", "-t", "="+novo+":", "-l", "/rename "+*x.Nome); err != nil {
		l.registrar(err)
		return
	}
	if _, err := l.executar("send-keys", "-t", "="+novo+":", "Enter"); err != nil {
		l.registrar(err)
		return
	}
	log.Printf("lançador: agente da sessão %s renomeado de %s para %s", x.Id, atual, novo)
}

func novoUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// ambiente devolve as atribuições NOME=valor do pedido, em ordem estável.
func (l *Lancador) ambiente(pedido api.PedidoDeLancamento) []string {
	valor := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	troca := strings.NewReplacer("{modelo}", valor(pedido.Modelo), "{esforco}", valor(pedido.Esforco))
	var nomes []string
	for nome := range l.cfg.Ambiente {
		nomes = append(nomes, nome)
	}
	sort.Strings(nomes)
	var atribuicoes []string
	for _, nome := range nomes {
		if v := troca.Replace(l.cfg.Ambiente[nome]); v != "" {
			atribuicoes = append(atribuicoes, nome+"="+v)
		}
	}
	return atribuicoes
}

func (l *Lancador) avisar(t api.Tarefa) error {
	nome, err := l.encontrar(t.Id)
	if err != nil {
		return err
	}
	if nome == "" {
		// Agente que o lançador não subiu (iniciado à mão e registrado depois): acha pelo chat da sessão.
		nome = l.encontrarPeloChat(*t.SessaoAtiva)
	}
	if nome == "" {
		log.Printf("lançador: tarefa %s saiu de em_andamento, mas não há agente vivo da sessão %s; encerre-a pelo frontend", t.Id, *t.SessaoAtiva)
		return nil
	}
	if _, err := l.executar("send-keys", "-t", "="+nome+":", "-l", preencher(l.cfg.PromptEncerrar, t)); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond) // o Enter separado do texto evita que vire quebra de linha
	if _, err := l.executar("send-keys", "-t", "="+nome+":", "Enter"); err != nil {
		return err
	}
	log.Printf("lançador: agente do tmux %s avisado para encerrar (tarefa %s em %s)", nome, t.Id, t.Status)
	return nil
}

// encontrar devolve a sessão tmux com REX_TAREFA (ou a legada REQUERIAL_TAREFA) igual ao id, ou "".
func (l *Lancador) encontrar(id string) (string, error) {
	saida, err := l.executar("list-sessions", "-F", "#{session_name}")
	if err != nil {
		return "", nil // sem servidor tmux, não há sessão
	}
	for _, nome := range strings.Fields(saida) {
		for _, variavel := range []string{VariavelTarefa, VariavelTarefaLegada} {
			valor, err := l.executar("show-environment", "-t", "="+nome, variavel)
			if err == nil && valor == variavel+"="+id {
				return nome, nil
			}
		}
	}
	return "", nil
}

// encontrarPeloChat acha a sessão tmux do agente da sessão de trabalho: o servidor MCP do agente
// carrega CLAUDE_CODE_SESSION_ID com o chat; sobe pelos processos pais até um painel do tmux.
func (l *Lancador) encontrarPeloChat(sessao string) string {
	if l.sessoes == nil {
		return ""
	}
	x, err := l.sessoes.Sessao(sessao)
	if err != nil || x.Chat == nil || *x.Chat == "" {
		return ""
	}
	return l.painelDoChat(*x.Chat)
}

// painelDoChat acha a sessão tmux do processo do agente com o chat dado. Vários processos carregam o
// chat (os servidores MCP do agente e tudo o que ele abriu); um programa que ele abriu e soltou, como
// um navegador, fica órfão e não sobe até painel nenhum, então cada um é tentado.
func (l *Lancador) painelDoChat(chat string) string {
	pids := l.processosDoChat(chat)
	if len(pids) == 0 {
		return ""
	}
	saida, err := l.executar("list-panes", "-a", "-F", "#{pane_pid} #{session_name}")
	if err != nil {
		return ""
	}
	paineis := map[int]string{}
	for _, linha := range strings.Split(saida, "\n") {
		var p int
		var nome string
		if _, err := fmt.Sscanf(linha, "%d %s", &p, &nome); err == nil {
			paineis[p] = nome
		}
	}
	for _, pid := range pids {
		for i := 0; i < 10 && pid > 1; i++ {
			if nome, ok := paineis[pid]; ok {
				return nome
			}
			pid = l.paiDe(pid)
		}
	}
	return ""
}

// processosDoChat procura em /proc os processos com CLAUDE_CODE_SESSION_ID igual ao chat.
func processosDoChat(chat string) []int {
	entradas, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	alvo := "CLAUDE_CODE_SESSION_ID=" + chat
	var pids []int
	for _, e := range entradas {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		ambiente, err := os.ReadFile("/proc/" + e.Name() + "/environ")
		if err != nil {
			continue
		}
		for _, v := range strings.Split(string(ambiente), "\x00") {
			if v == alvo {
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids
}

func paiDe(pid int) int {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	texto := string(stat)
	campos := strings.Fields(texto[strings.LastIndexByte(texto, ')')+1:])
	if len(campos) < 2 {
		return 0
	}
	pai, _ := strconv.Atoi(campos[1])
	return pai
}

func (l *Lancador) nomeLivre(base string) (string, error) {
	nome := base
	for i := 2; i < 100; i++ {
		if _, err := l.executar("has-session", "-t", "="+nome); err != nil {
			return nome, nil
		}
		nome = fmt.Sprintf("%s-%d", base, i)
	}
	return "", fmt.Errorf("sem nome livre para o tmux a partir de %q", base)
}

func (l *Lancador) registrar(err error) {
	if err != nil {
		log.Printf("lançador: %v", err)
	}
}

func preencher(modelo string, t api.Tarefa) string {
	return strings.NewReplacer("{tarefa}", t.Id, "{titulo}", t.Titulo, "{status}", string(t.Status)).Replace(modelo)
}

// palavrasVazias e verbosGenericos não dizem o assunto: o nome derivado do título os pula.
var palavrasVazias = map[string]bool{
	"dos": true, "das": true, "com": true, "para": true, "por": true, "uma": true, "nos": true, "nas": true,
	"the": true, "and": true, "for": true,
}

var verbosGenericos = map[string]bool{
	"retomar": true, "criar": true, "revisar": true, "definir": true, "implementar": true, "fazer": true,
	"ajustar": true, "corrigir": true, "investigar": true, "analisar": true, "verificar": true, "conferir": true,
	"melhorar": true, "atualizar": true, "documentar": true, "priorizar": true, "configurar": true,
	"implantar": true, "migrar": true, "montar": true, "preparar": true, "resolver": true, "terminar": true,
	"concluir": true, "finalizar": true, "continuar": true, "acompanhar": true, "validar": true, "testar": true,
	"apoiar": true, "suporte": true, "retomada": true, "revisao": true, "implantacao": true, "criacao": true,
}

// tamanhoDoNomeDerivado limita o nome tirado do título: sem quem escolha, curto é mais legível.
const tamanhoDoNomeDerivado = 30

// nomeCurto deriva do título um nome de sessão tmux, quando quem lança não deu um: minúsculas, sem
// acento, sem palavras vazias nem verbos genéricos ("retomar", "revisar"…), até 30 caracteres.
func nomeCurto(titulo string) string {
	var palavras []string
	for _, p := range api.PalavrasDoNome(titulo) {
		if len(p) >= 3 && !palavrasVazias[p] && !verbosGenericos[p] {
			palavras = append(palavras, p)
		}
	}
	nome := api.JuntarNome(palavras, tamanhoDoNomeDerivado)
	if nome == "" {
		nome = "tarefa"
	}
	return nome
}
