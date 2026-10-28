// Package recursos mede a memória e a CPU da máquina do lançador, para não subir um agente que
// estoure a RAM: sem swap, quem estoura não fica lento, o kernel mata um processo.
//
// Lê só o /proc e o docker. Dos processos guarda o nome curto (comm), nunca a linha de comando,
// que pode carregar tokens; a única exceção é o valor de --remote-control, o nome da sessão.
package recursos

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config vem da configuração do lançador, na chave "recursos".
type Config struct {
	// MemoriaMinimaMB é a RAM disponível abaixo da qual o lançamento é recusado sem forcar:
	// a sessão nova (~1 GB) mais a folga. Padrão: 2500.
	MemoriaMinimaMB int `json:"memoria_minima_mb"`
	// Itens são as coisas grandes que se pode descer para liberar memória (um ambiente docker, a
	// sessão do agente navegador, a Steam), cada uma com o jeito de reconhecê-la e de descê-la.
	Itens []Item `json:"itens"`
	// Intervalo da amostra de CPU. Padrão: 1 s.
	Intervalo time.Duration `json:"-"`
}

// Medidor mede a máquina em que roda.
type Medidor struct {
	cfg  Config
	proc string
	// docker lista os containers com projeto e RSS; substituível nos testes.
	docker func(ctx context.Context) ([]Container, error)
}

// Novo preenche os padrões.
func Novo(cfg Config) *Medidor {
	if cfg.MemoriaMinimaMB <= 0 {
		cfg.MemoriaMinimaMB = 2500
	}
	if cfg.Intervalo <= 0 {
		cfg.Intervalo = time.Second
	}
	return &Medidor{cfg: cfg, proc: "/proc", docker: containersDoDocker}
}

// Item é uma coisa grande da máquina. Reconhece-se por um destes (o primeiro preenchido):
// Processos, nomes curtos (comm) exatos, ou prefixos terminados em "*", contando cada um com os
// filhos; ProjetoDocker, o projeto do compose; Sessao, o nome (remote-control) de uma sessão do
// Claude Code, com os filhos dela. Desce-se pausando Tarefa (o agente dela encerra e leva os filhos
// junto) ou rodando Comando.
type Item struct {
	Nome          string   `json:"nome"`
	Descricao     string   `json:"descricao"`
	Processos     []string `json:"processos"`
	ProjetoDocker string   `json:"projeto_docker"`
	Sessao        string   `json:"sessao"`
	Tarefa        string   `json:"tarefa"`
	Comando       []string `json:"comando"`
}

// Item procura um item pelo nome.
func (c Config) Item(nome string) (Item, bool) {
	for _, i := range c.Itens {
		if i.Nome == nome {
			return i, true
		}
	}
	return Item{}, false
}

// Config devolve a configuração com os padrões.
func (m *Medidor) Config() Config { return m.cfg }

// Medicao é o retrato da máquina num momento.
type Medicao struct {
	TotalMB      int
	DisponivelMB int
	Nucleos      int
	Carga1Min    float64
	// Sessoes são os Claude Code abertos, cada um com os filhos (servidores MCP).
	Sessoes []Sessao
	// Itens são os da configuração, na mesma ordem.
	Itens      []Grupo
	Containers []Container
	// ErroDocker diz por que os containers não foram medidos, se não foram.
	ErroDocker      string
	MaioresPorRAM   []Processo
	MaioresPorCPU   []Processo
	MemoriaMinimaMB int
}

type Sessao struct {
	Chat string // CLAUDE_CODE_SESSION_ID
	Nome string // --remote-control
	MB   int
}

type Grupo struct {
	Nome   string
	Aberto bool
	MB     int
}

type Container struct {
	Nome    string
	Projeto string // com.docker.compose.project
	MB      int
}

type Processo struct {
	Pid  int
	Nome string
	MB   int
	CPU  float64 // % de um núcleo na amostra
}

// processo é o que se lê do /proc de cada pid.
type processo struct {
	pid, pai int
	nome     string
	rss      int64 // bytes: PSS quando legível, senão RSS
	ticks    uint64
}

// Descer roda o comando do item e devolve a saída, com no máximo 3 min. Pausar a tarefa do item é
// do servidor.
func (m *Medidor) Descer(ctx context.Context, item Item) (string, error) {
	if len(item.Comando) == 0 {
		return "", nil
	}
	ctx, cancelar := context.WithTimeout(ctx, 3*time.Minute)
	defer cancelar()
	saida, err := exec.CommandContext(ctx, item.Comando[0], item.Comando[1:]...).CombinedOutput()
	texto := strings.TrimSpace(string(saida))
	if len(texto) > 2000 {
		texto = "…" + texto[len(texto)-2000:]
	}
	if err != nil {
		return texto, fmt.Errorf("descer %s: %v: %s", item.Nome, err, texto)
	}
	return texto, nil
}

// Medir lê a memória, a árvore de processos (duas vezes, para a CPU) e o docker em paralelo.
func (m *Medidor) Medir(ctx context.Context) (Medicao, error) {
	med := Medicao{MemoriaMinimaMB: m.cfg.MemoriaMinimaMB}
	var containers []Container
	var erroDocker error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c, cancelar := context.WithTimeout(ctx, 10*time.Second)
		defer cancelar()
		containers, erroDocker = m.docker(c)
	}()

	antes := m.processos(false)
	select {
	case <-time.After(m.cfg.Intervalo):
	case <-ctx.Done():
		return med, ctx.Err()
	}
	depois := m.processos(true)
	if err := m.memoria(&med); err != nil {
		return med, err
	}
	med.Nucleos = runtime.NumCPU()
	if b, err := os.ReadFile(m.proc + "/loadavg"); err == nil {
		if campos := strings.Fields(string(b)); len(campos) > 0 {
			med.Carga1Min, _ = strconv.ParseFloat(campos[0], 64)
		}
	}

	filhos := map[int][]int{}
	for _, p := range depois {
		filhos[p.pai] = append(filhos[p.pai], p.pid)
	}
	arvore := func(raiz int) []int {
		var pids []int
		pilha := []int{raiz}
		for len(pilha) > 0 {
			p := pilha[len(pilha)-1]
			pilha = pilha[:len(pilha)-1]
			pids = append(pids, p)
			pilha = append(pilha, filhos[p]...)
		}
		return pids
	}
	soma := func(pids []int) int {
		var total int64
		for _, p := range pids {
			total += depois[p].rss
		}
		return mb(total)
	}
	// raiz: o primeiro do grupo na linhagem (um claude cujo pai não é claude, e assim por diante).
	raizes := func(eh func(p processo) bool) []int {
		var r []int
		for _, p := range depois {
			if eh(p) && !eh(depois[p.pai]) {
				r = append(r, p.pid)
			}
		}
		sort.Ints(r)
		return r
	}

	for _, raiz := range raizes(func(p processo) bool { return p.nome == "claude" }) {
		pids := arvore(raiz)
		s := Sessao{Chat: m.opcao(raiz, "--session-id"), Nome: m.opcao(raiz, "--remote-control"), MB: soma(pids)}
		// Sem --session-id (chat aberto à mão), o chat está no ambiente dos filhos.
		for _, p := range pids[1:] {
			if s.Chat != "" {
				break
			}
			s.Chat = m.variavel(p, "CLAUDE_CODE_SESSION_ID")
		}
		med.Sessoes = append(med.Sessoes, s)
	}
	sort.Slice(med.Sessoes, func(i, j int) bool { return med.Sessoes[i].MB > med.Sessoes[j].MB })

	grupoDeProcessos := func(nomes []string) Grupo {
		var g Grupo
		eh := func(p processo) bool {
			for _, n := range nomes {
				if p.nome == n || strings.HasSuffix(n, "*") && strings.HasPrefix(p.nome, strings.TrimSuffix(n, "*")) {
					return true
				}
			}
			return false
		}
		for _, raiz := range raizes(eh) {
			g.Aberto = true
			g.MB += soma(arvore(raiz))
		}
		return g
	}

	var todos []Processo
	eu := os.Getpid()
	for pid, p := range depois {
		if pid == eu {
			continue // a CPU da própria medição não é de ninguém
		}
		x := Processo{Pid: pid, Nome: p.nome, MB: mb(p.rss)}
		if a, ok := antes[pid]; ok && p.ticks >= a.ticks {
			x.CPU = float64(p.ticks-a.ticks) / ticksPorSegundo / m.cfg.Intervalo.Seconds() * 100
		}
		todos = append(todos, x)
	}
	med.MaioresPorRAM = maiores(todos, func(a, b Processo) bool { return a.MB > b.MB })
	med.MaioresPorCPU = maiores(todos, func(a, b Processo) bool { return a.CPU > b.CPU })

	wg.Wait()
	med.Containers = containers
	if erroDocker != nil {
		med.ErroDocker = erroDocker.Error()
	}
	for _, item := range m.cfg.Itens {
		var g Grupo
		switch {
		case len(item.Processos) > 0:
			g = grupoDeProcessos(item.Processos)
		case item.ProjetoDocker != "":
			for _, c := range containers {
				if c.Projeto == item.ProjetoDocker {
					g.Aberto = true
					g.MB += c.MB
				}
			}
		case item.Sessao != "":
			for _, x := range med.Sessoes {
				if x.Nome == item.Sessao {
					g.Aberto = true
					g.MB += x.MB
				}
			}
		}
		g.Nome = item.Nome
		med.Itens = append(med.Itens, g)
	}
	return med, nil
}

// ticksPorSegundo é o USER_HZ do Linux, 100 em todas as arquiteturas comuns.
const ticksPorSegundo = 100

const quantos = 15

func maiores(todos []Processo, antes func(a, b Processo) bool) []Processo {
	c := append([]Processo(nil), todos...)
	sort.SliceStable(c, func(i, j int) bool {
		if antes(c[i], c[j]) != antes(c[j], c[i]) {
			return antes(c[i], c[j])
		}
		return c[i].Pid < c[j].Pid
	})
	if len(c) > quantos {
		c = c[:quantos]
	}
	return c
}

func mb(bytes int64) int { return int(bytes / (1 << 20)) }

func (m *Medidor) memoria(med *Medicao) error {
	f, err := os.Open(m.proc + "/meminfo")
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		campos := strings.Fields(sc.Text())
		if len(campos) < 2 {
			continue
		}
		kb, _ := strconv.Atoi(campos[1])
		switch campos[0] {
		case "MemTotal:":
			med.TotalMB = kb / 1024
		case "MemAvailable:":
			med.DisponivelMB = kb / 1024
		}
	}
	if med.TotalMB == 0 {
		return fmt.Errorf("não consegui ler a memória em %s/meminfo", m.proc)
	}
	return sc.Err()
}

// processos lê pid, pai, nome, memória e tempo de CPU de todos os processos. Um processo que some
// no meio da leitura fica de fora. Com pss, a memória é o PSS (a memória compartilhada dividida
// entre quem a usa), que não conta duas vezes as bibliotecas e os processos irmãos do Chromium e da
// Steam; sem permissão para lê-lo (processos de outro usuário), fica o RSS.
func (m *Medidor) processos(pss bool) map[int]processo {
	out := map[int]processo{}
	entradas, err := os.ReadDir(m.proc)
	if err != nil {
		return out
	}
	pagina := int64(os.Getpagesize())
	for _, e := range entradas {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(fmt.Sprintf("%s/%d/stat", m.proc, pid))
		if err != nil {
			continue
		}
		// O nome vem entre parênteses e pode ter espaços; os campos seguem o último ")".
		ini, fim := bytes.IndexByte(stat, '('), bytes.LastIndexByte(stat, ')')
		if ini < 0 || fim < ini {
			continue
		}
		campos := strings.Fields(string(stat[fim+1:]))
		// campos[0] é o estado (campo 3); ppid é o 4, utime o 14, stime o 15, rss o 24.
		if len(campos) < 22 {
			continue
		}
		pai, _ := strconv.Atoi(campos[1])
		utime, _ := strconv.ParseUint(campos[11], 10, 64)
		stime, _ := strconv.ParseUint(campos[12], 10, 64)
		rss, _ := strconv.ParseInt(campos[21], 10, 64)
		p := processo{pid: pid, pai: pai, nome: string(stat[ini+1 : fim]), rss: rss * pagina, ticks: utime + stime}
		if pss {
			if v, ok := m.pss(pid); ok {
				p.rss = v
			}
		}
		out[pid] = p
	}
	return out
}

func (m *Medidor) pss(pid int) (int64, bool) {
	b, err := os.ReadFile(fmt.Sprintf("%s/%d/smaps_rollup", m.proc, pid))
	if err != nil {
		return 0, false
	}
	for _, linha := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(linha, "Pss:"); ok {
			kb, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64)
			return kb * 1024, err == nil
		}
	}
	return 0, false
}

func (m *Medidor) argumentos(pid int) []string {
	b, err := os.ReadFile(fmt.Sprintf("%s/%d/cmdline", m.proc, pid))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
}

// opcao devolve o valor de --nome valor ou --nome=valor.
func (m *Medidor) opcao(pid int, nome string) string {
	args := m.argumentos(pid)
	for i, a := range args {
		if a == nome && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, nome+"="); ok {
			return v
		}
	}
	return ""
}

func (m *Medidor) variavel(pid int, nome string) string {
	b, err := os.ReadFile(fmt.Sprintf("%s/%d/environ", m.proc, pid))
	if err != nil {
		return ""
	}
	for _, kv := range strings.Split(string(b), "\x00") {
		if v, ok := strings.CutPrefix(kv, nome+"="); ok {
			return v
		}
	}
	return ""
}

// containersDoDocker junta o docker ps (nome e projeto do compose) com o docker stats (memória).
func containersDoDocker(ctx context.Context) ([]Container, error) {
	ps, err := exec.CommandContext(ctx, "docker", "ps", "--format", `{{.Names}}`+"\t"+`{{.Label "com.docker.compose.project"}}`).Output()
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	projetos := map[string]string{}
	for _, linha := range strings.Split(strings.TrimSpace(string(ps)), "\n") {
		if nome, projeto, _ := strings.Cut(linha, "\t"); nome != "" {
			projetos[nome] = projeto
		}
	}
	if len(projetos) == 0 {
		return nil, nil
	}
	stats, err := exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "{{.Name}}\t{{.MemUsage}}").Output()
	if err != nil {
		return nil, fmt.Errorf("docker stats: %w", err)
	}
	var out []Container
	for _, linha := range strings.Split(strings.TrimSpace(string(stats)), "\n") {
		nome, uso, ok := strings.Cut(linha, "\t")
		if !ok {
			continue
		}
		usado, _, _ := strings.Cut(uso, " / ")
		out = append(out, Container{Nome: nome, Projeto: projetos[nome], MB: megabytes(usado)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MB > out[j].MB })
	return out, nil
}

// megabytes lê o tamanho do docker stats ("512KiB", "1.2GiB", "300MB").
func megabytes(s string) int {
	s = strings.TrimSpace(s)
	unidades := []struct {
		sufixo string
		mb     float64
	}{{"GiB", 1024}, {"MiB", 1}, {"KiB", 1.0 / 1024}, {"GB", 1000 * 1000 * 1000 / float64(1<<20)},
		{"MB", 1000 * 1000 / float64(1<<20)}, {"kB", 1000 / float64(1<<20)}, {"B", 1 / float64(1<<20)}}
	for _, u := range unidades {
		if v, ok := strings.CutSuffix(s, u.sufixo); ok {
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return 0
			}
			return int(n * u.mb)
		}
	}
	return 0
}
