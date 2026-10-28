package recursos

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// procFalso monta um /proc com os processos dados: pid, pai, nome, PSS em MB, argumentos e ambiente.
type procFalso struct {
	pid, pai int
	nome     string
	mb       int
	args     []string
	ambiente []string
}

func montarProc(t *testing.T, ps []procFalso) string {
	t.Helper()
	dir := t.TempDir()
	escrever := func(caminho, conteudo string) {
		if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(caminho, []byte(conteudo), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	escrever(filepath.Join(dir, "meminfo"), "MemTotal:       16220000 kB\nMemFree:  100 kB\nMemAvailable:    2048000 kB\n")
	escrever(filepath.Join(dir, "loadavg"), "1.50 1.00 0.50 2/900 12345\n")
	for _, p := range ps {
		base := filepath.Join(dir, fmt.Sprint(p.pid))
		// 52 campos; utime e stime a zero, rss em páginas (que o PSS substitui).
		campos := make([]string, 50)
		for i := range campos {
			campos[i] = "0"
		}
		campos[0], campos[1] = "S", fmt.Sprint(p.pai)
		escrever(filepath.Join(base, "stat"), fmt.Sprintf("%d (%s) %s\n", p.pid, p.nome, strings.Join(campos, " ")))
		escrever(filepath.Join(base, "smaps_rollup"), fmt.Sprintf("Rss: 1 kB\nPss:   %d kB\n", p.mb*1024))
		escrever(filepath.Join(base, "cmdline"), strings.Join(p.args, "\x00")+"\x00")
		escrever(filepath.Join(base, "environ"), strings.Join(p.ambiente, "\x00")+"\x00")
	}
	return dir
}

func TestMedirAgrupa(t *testing.T) {
	proc := montarProc(t, []procFalso{
		{pid: 1, nome: "systemd", mb: 10},
		{pid: 10, pai: 1, nome: "claude", mb: 400, args: []string{"claude", "--session-id", "chat-a", "--remote-control", "trabalho-a"}},
		{pid: 11, pai: 10, nome: "node", mb: 200},
		{pid: 12, pai: 10, nome: "claude", mb: 50}, // subprocesso: conta na sessão de cima
		{pid: 20, pai: 1, nome: "claude", mb: 300, args: []string{"claude"}},
		{pid: 21, pai: 20, nome: "node", mb: 100, ambiente: []string{"CLAUDE_CODE_SESSION_ID=chat-b"}},
		{pid: 13, pai: 10, nome: "chromium", mb: 500}, // o navegador que o MCP da sessão abriu
		{pid: 30, pai: 1, nome: "chrome", mb: 500},
		{pid: 31, pai: 30, nome: "chrome", mb: 700},
		{pid: 32, pai: 1, nome: "chrome-devtools", mb: 900}, // não é o Chrome: nome exato
		{pid: 50, pai: 1, nome: "steam", mb: 400},
		{pid: 51, pai: 50, nome: "StardewValley", mb: 600},
		{pid: 60, pai: 1, nome: "pv-adverb", mb: 10},
		{pid: 61, pai: 60, nome: "steamwebhelper", mb: 500}, // por prefixo, mesmo fora da árvore da steam
	})
	m := Novo(Config{Intervalo: time.Millisecond, Itens: []Item{
		{Nome: "agente-navegador", Sessao: "trabalho-a"},
		{Nome: "steam", Processos: []string{"steam*"}},
		{Nome: "chrome", Processos: []string{"chrome"}},
		{Nome: "app", ProjetoDocker: "app"},
		{Nome: "nada", Processos: []string{"steamx"}},
	}})
	m.proc = proc
	m.docker = func(context.Context) ([]Container, error) {
		return []Container{{Nome: "db", Projeto: "app", MB: 300}}, nil
	}
	med, err := m.Medir(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if med.TotalMB != 15839 || med.DisponivelMB != 2000 || med.MemoriaMinimaMB != 2500 || med.Carga1Min != 1.5 {
		t.Fatalf("memória: %+v", med)
	}
	esperadas := []Sessao{{Chat: "chat-a", Nome: "trabalho-a", MB: 1150}, {Chat: "chat-b", MB: 400}}
	if fmt.Sprint(med.Sessoes) != fmt.Sprint(esperadas) {
		t.Fatalf("sessões: %+v", med.Sessoes)
	}
	itens := []Grupo{{"agente-navegador", true, 1150}, {"steam", true, 1500}, {"chrome", true, 1200}, {"app", true, 300}, {"nada", false, 0}}
	if fmt.Sprint(med.Itens) != fmt.Sprint(itens) {
		t.Fatalf("itens: %+v", med.Itens)
	}
	if len(med.Containers) != 1 || len(med.MaioresPorRAM) != 14 || med.MaioresPorRAM[0].Nome != "chrome-devtools" || med.MaioresPorRAM[0].Pid != 32 {
		t.Fatalf("containers %+v, maiores %+v", med.Containers, med.MaioresPorRAM)
	}
}

func TestDescerRodaOComando(t *testing.T) {
	m := Novo(Config{})
	saida, err := m.Descer(context.Background(), Item{Nome: "x", Comando: []string{"sh", "-c", "echo fechado"}})
	if err != nil || saida != "fechado" {
		t.Fatalf("%q %v", saida, err)
	}
	_, err = m.Descer(context.Background(), Item{Nome: "travado", Comando: []string{"sh", "-c", "echo o lock é de outro agente; exit 1"}})
	if err == nil || !strings.Contains(err.Error(), "o lock é de outro agente") {
		t.Fatalf("recusa: %v", err)
	}
}

func TestMegabytes(t *testing.T) {
	for entrada, esperado := range map[string]int{"1.5GiB": 1536, "300MiB": 300, "2048KiB": 2, "0B": 0, "1GB": 953, "lixo": 0} {
		if got := megabytes(entrada); got != esperado {
			t.Errorf("megabytes(%q) = %d, esperado %d", entrada, got, esperado)
		}
	}
}
