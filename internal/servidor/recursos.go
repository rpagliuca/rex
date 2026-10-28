package servidor

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/rpagliuca/rex/api"
	"github.com/rpagliuca/rex/internal/recursos"
)

// Medidor mede a máquina do lançador.
type Medidor interface {
	Medir(ctx context.Context) (recursos.Medicao, error)
	Descer(ctx context.Context, item recursos.Item) (string, error)
	Config() recursos.Config
}

// ComMedidor liga a medição de recursos: o endpoint /recursos e a recusa de lançar agente com pouca
// memória.
func (s *Servidor) ComMedidor(m Medidor) {
	s.medidor = m
}

func (s *Servidor) RecursosDaMaquina(w http.ResponseWriter, r *http.Request) {
	if s.medidor == nil {
		responderErro(w, falha(http.StatusNotFound, "a medição de recursos não está configurada"))
		return
	}
	x, err := s.medir(r.Context())
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusOK, x)
}

// conferirMemoria recusa um lançamento quando a RAM disponível está abaixo do mínimo, com a medição
// na recusa, para quem pediu decidir o que fechar ou confirmar com forcar.
func (s *Servidor) conferirMemoria(ctx context.Context, forcar bool) error {
	if s.medidor == nil || forcar {
		return nil
	}
	x, err := s.medir(ctx)
	if err != nil {
		return fmt.Errorf("medir a memória antes de lançar: %w", err)
	}
	if !x.AbaixoDoMinimo {
		return nil
	}
	return &erroHTTP{status: http.StatusConflict, recursos: &x, mensagem: "pouca memória para subir mais um agente: " +
		"confirme com o Rafael e peça de novo com forcar, ou libere memória antes.\n" + x.Resumo}
}

// medir junta a medição da máquina às sessões ativas do rex, pelo chat de cada Claude Code.
func (s *Servidor) medir(ctx context.Context) (api.Recursos, error) {
	m, err := s.medidor.Medir(ctx)
	if err != nil {
		return api.Recursos{}, err
	}
	ativas, err := listarSessoes(s.db, `WHERE s.status = 'ativa' ORDER BY s.id`)
	if err != nil {
		return api.Recursos{}, err
	}
	porChat := map[string]api.Sessao{}
	for _, x := range ativas {
		if x.Chat != nil {
			porChat[*x.Chat] = x
		}
	}
	r := api.Recursos{
		MedidoEm:            s.agora().UTC(),
		MemoriaTotalMb:      m.TotalMB,
		MemoriaDisponivelMb: m.DisponivelMB,
		MemoriaMinimaMb:     m.MemoriaMinimaMB,
		AbaixoDoMinimo:      m.DisponivelMB < m.MemoriaMinimaMB,
		Nucleos:             m.Nucleos,
		Carga1min:           float32(m.Carga1Min),
		Sessoes:             []api.SessaoNaMaquina{},
		Itens:               []api.ItemDaMaquina{},
		Docker:              []api.ProjetoDocker{},
		MaioresPorMemoria:   processosNaMaquina(m.MaioresPorRAM),
		MaioresPorCpu:       processosNaMaquina(m.MaioresPorCPU),
	}
	for _, x := range m.Sessoes {
		sm := api.SessaoNaMaquina{Nome: x.Nome, MemoriaMb: x.MB}
		if ativa, ok := porChat[x.Chat]; ok && x.Chat != "" {
			sm.Sessao = &ativa.Id
			sm.Tarefas = &ativa.Tarefas
			sm.Aguardando = ativa.Aguardando
			if sm.Nome == "" && ativa.Nome != nil {
				sm.Nome = *ativa.Nome
			}
		}
		r.Sessoes = append(r.Sessoes, sm)
	}
	itens := s.medidor.Config().Itens
	for i, g := range m.Itens {
		r.Itens = append(r.Itens, api.ItemDaMaquina{Nome: g.Nome, Descricao: itens[i].Descricao, Aberto: g.Aberto, MemoriaMb: g.MB})
	}
	projetos := map[string]int{}
	for _, c := range m.Containers {
		i, ok := projetos[c.Projeto]
		if !ok {
			i = len(r.Docker)
			projetos[c.Projeto] = i
			r.Docker = append(r.Docker, api.ProjetoDocker{Projeto: c.Projeto, Containers: []struct {
				MemoriaMb int    `json:"memoria_mb"`
				Nome      string `json:"nome"`
			}{}})
		}
		r.Docker[i].MemoriaMb += c.MB
		r.Docker[i].Containers = append(r.Docker[i].Containers, struct {
			MemoriaMb int    `json:"memoria_mb"`
			Nome      string `json:"nome"`
		}{c.MB, c.Nome})
	}
	sort.SliceStable(r.Docker, func(i, j int) bool { return r.Docker[i].MemoriaMb > r.Docker[j].MemoriaMb })
	if m.ErroDocker != "" {
		r.ErroDocker = &m.ErroDocker
	}
	r.Resumo = resumoDosRecursos(r, s.agora())
	return r, nil
}

// DescerItem desce um item para liberar memória: pausa a tarefa dele, se tiver, e roda o comando.
func (s *Servidor) DescerItem(w http.ResponseWriter, r *http.Request) {
	if s.medidor == nil {
		responderErro(w, falha(http.StatusNotFound, "a medição de recursos não está configurada"))
		return
	}
	var pedido api.PedidoDeDescida
	if err := ler(r, &pedido); err != nil {
		responderErro(w, err)
		return
	}
	cfg := s.medidor.Config()
	item, ok := cfg.Item(pedido.Item)
	if !ok {
		var nomes []string
		for _, i := range cfg.Itens {
			nomes = append(nomes, i.Nome)
		}
		responderErro(w, falha(http.StatusNotFound, "item desconhecido: %q (há: %s)", pedido.Item, strings.Join(nomes, ", ")))
		return
	}
	var feito []string
	if item.Tarefa != "" {
		f, err := s.pausarParaDescer(r, item)
		if err != nil {
			responderErro(w, err)
			return
		}
		feito = append(feito, f)
	}
	if len(item.Comando) > 0 {
		saida, err := s.medidor.Descer(r.Context(), item)
		if err != nil {
			responderErro(w, falha(http.StatusConflict, "%v", err))
			return
		}
		if saida == "" {
			saida = "comando rodou sem saída"
		}
		feito = append(feito, saida)
	}
	responder(w, http.StatusOK, api.Descida{Item: item.Nome, Feito: strings.Join(feito, "\n")})
}

// pausarParaDescer pausa a tarefa do item, se está em andamento: o lançador pede ao agente que
// encerre, e o agente leva os filhos dele (servidores MCP, navegador) junto.
func (s *Servidor) pausarParaDescer(r *http.Request, item recursos.Item) (string, error) {
	n, err := numero(item.Tarefa, "T")
	if err != nil {
		return "", err
	}
	var alterada api.Tarefa
	var anterior api.StatusTarefa
	var quem string
	err = s.emTransacao(func(tx *sql.Tx) (err error) {
		if quem, err = autor(tx, r); err != nil {
			return err
		}
		atual, err := carregarTarefa(tx, n)
		if err != nil {
			return err
		}
		anterior = atual.Status
		if anterior != api.EmAndamento {
			alterada = atual
			return nil
		}
		if _, err := tx.Exec(`UPDATE tarefas SET status = ?, atualizada_em = ? WHERE id = ?`, string(api.Pausada), s.momento(), n); err != nil {
			return err
		}
		if alterada, err = carregarTarefa(tx, n); err != nil {
			return err
		}
		return s.registrarEvento(tx, quem, alterada.Id, "tarefa_alterada", map[string]any{"status": api.Pausada, "descida": item.Nome})
	})
	if err != nil {
		return "", err
	}
	if anterior != api.EmAndamento {
		return fmt.Sprintf("a tarefa %s já estava %s; nada a pausar", alterada.Id, anterior), nil
	}
	s.avisarMudanca(MudancaDeStatus{Tarefa: alterada, Anterior: anterior, Autor: quem})
	msg := fmt.Sprintf("tarefa %s pausada", alterada.Id)
	if alterada.SessaoAtiva != nil {
		msg += fmt.Sprintf("; o agente da sessão %s vai encerrar e liberar a memória em alguns segundos", *alterada.SessaoAtiva)
	}
	return msg, nil
}

func processosNaMaquina(ps []recursos.Processo) []api.ProcessoNaMaquina {
	out := []api.ProcessoNaMaquina{}
	for _, p := range ps {
		out = append(out, api.ProcessoNaMaquina{Pid: p.Pid, Nome: p.Nome, MemoriaMb: p.MB, Cpu: float32(int(p.CPU*10)) / 10})
	}
	return out
}

func gb(mb int) string {
	return strings.Replace(fmt.Sprintf("%.1f GB", float64(mb)/1024), ".", ",", 1)
}

// resumoDosRecursos diz a medição em poucas linhas, do jeito que o Rafael lê (ou ouve, na voz).
func resumoDosRecursos(r api.Recursos, agora time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Memória disponível: %s de %s (mínimo para lançar agente: %s).\n",
		gb(r.MemoriaDisponivelMb), gb(r.MemoriaTotalMb), gb(r.MemoriaMinimaMb))
	total := 0
	for _, x := range r.Sessoes {
		total += x.MemoriaMb
	}
	fmt.Fprintf(&b, "Sessões do Claude Code abertas: %d, somando %s.\n", len(r.Sessoes), gb(total))
	if len(r.Itens) > 0 {
		var abertos, fechados []string
		for _, i := range r.Itens {
			if i.Aberto {
				abertos = append(abertos, fmt.Sprintf("%s %s", i.Nome, gb(i.MemoriaMb)))
			} else {
				fechados = append(fechados, i.Nome)
			}
		}
		if len(abertos) > 0 {
			fmt.Fprintf(&b, "Abertos, e o que descer libera: %s.\n", strings.Join(abertos, "; "))
		}
		if len(fechados) > 0 {
			fmt.Fprintf(&b, "Fechados: %s.\n", strings.Join(fechados, ", "))
		}
	}
	switch {
	case r.ErroDocker != nil:
		fmt.Fprintf(&b, "Docker: não medido (%s).\n", *r.ErroDocker)
	case len(r.Docker) == 0:
		b.WriteString("Docker: nenhum container rodando.\n")
	default:
		var partes []string
		for _, p := range r.Docker {
			nome := p.Projeto
			if nome == "" {
				nome = "avulsos"
			}
			partes = append(partes, fmt.Sprintf("%s %s", nome, gb(p.MemoriaMb)))
		}
		fmt.Fprintf(&b, "Docker: %s.\n", strings.Join(partes, "; "))
	}
	// As que esperam o Rafael há mais tempo são as candidatas a encerrar.
	var esperando []api.SessaoNaMaquina
	for _, x := range r.Sessoes {
		if x.Aguardando != nil {
			esperando = append(esperando, x)
		}
	}
	sort.SliceStable(esperando, func(i, j int) bool { return esperando[i].Aguardando.Desde.Before(esperando[j].Aguardando.Desde) })
	if len(esperando) > 0 {
		var partes []string
		for _, x := range esperando {
			partes = append(partes, fmt.Sprintf("%s (%s, há %s, %s)", x.Nome, *x.Sessao, duracao(agora.Sub(x.Aguardando.Desde)), gb(x.MemoriaMb)))
		}
		fmt.Fprintf(&b, "Esperando o Rafael, da mais antiga: %s.\n", strings.Join(partes, "; "))
	}
	if len(r.MaioresPorCpu) > 0 {
		p := r.MaioresPorCpu[0]
		carga := strings.Replace(fmt.Sprintf("%.1f", r.Carga1min), ".", ",", 1)
		fmt.Fprintf(&b, "CPU: carga %s em %d núcleos; o processo mais ocupado é %s, com %.0f%% de um núcleo.",
			carga, r.Nucleos, p.Nome, p.Cpu)
	}
	return strings.TrimSpace(b.String())
}

func duracao(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%d d", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	default:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
}
