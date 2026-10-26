package integracao

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rpagliuca/rex/api"
	"github.com/rpagliuca/rex/internal/banco"
	"github.com/rpagliuca/rex/internal/servidor"
)

func novoServidor(t *testing.T) (*servidor.Servidor, *httptest.Server) {
	t.Helper()
	db, err := banco.Abrir(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	s := servidor.Novo(db)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(func() { srv.Close(); db.Close() })
	return s, srv
}

func postar(t *testing.T, url string, corpo any) {
	t.Helper()
	texto, _ := json.Marshal(corpo)
	resp, err := http.Post(url, "application/json", bytes.NewReader(texto))
	if err != nil || resp.StatusCode >= 300 {
		t.Fatalf("POST %s: %v %v", url, err, resp)
	}
	resp.Body.Close()
}

// esperar repete cond até ela valer, por no máximo 5 s.
func esperar(t *testing.T, cond func() bool) {
	t.Helper()
	for limite := time.Now().Add(5 * time.Second); time.Now().Before(limite); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("a condição não aconteceu a tempo")
}

func TestGanchosEntreganOsEventosAssinados(t *testing.T) {
	s, api_ := novoServidor(t)
	var mu sync.Mutex
	var recebidas []api.Notificacao
	var cabecalho string
	falhas := 1 // a primeira chamada falha: o gancho tenta de novo
	destino := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if falhas > 0 {
			falhas--
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		var n api.Notificacao
		json.NewDecoder(r.Body).Decode(&n)
		recebidas = append(recebidas, n)
		cabecalho = r.Header.Get("Authorization")
	}))
	defer destino.Close()

	saida := filepath.Join(t.TempDir(), "saida")
	i, err := Nova(Config{Ganchos: []Gancho{
		{Nome: "nuvem", Eventos: []string{"lembrete_*"}, Webhook: &Webhook{URL: destino.URL, Cabecalhos: map[string]string{"Authorization": "env:TOKEN"}}},
		{Nome: "barra", Eventos: []string{"tarefa_criada"}, Comando: []string{"sh", "-c", `cat > "$0"; echo >> "$0"; echo "$1" >> "$0"`, saida, "{entidade}"}},
	}}, func(nome string) string { return map[string]string{"TOKEN": "Bearer x"}[nome] })
	if err != nil {
		t.Fatal(err)
	}
	i.intervalo = 10 * time.Millisecond
	i.tentativas = []time.Duration{10 * time.Millisecond}

	// Eventos de antes do gancho existir não são entregues.
	postar(t, api_.URL+"/tarefas", api.NovaTarefa{Titulo: "antiga", Organizacao: "Exemplo"})

	ctx, cancelar := context.WithCancel(context.Background())
	defer cancelar()
	i.Conectar(ctx, s)
	time.Sleep(50 * time.Millisecond)

	postar(t, api_.URL+"/tarefas", api.NovaTarefa{Titulo: "nova", Organizacao: "Exemplo"})
	postar(t, api_.URL+"/lembretes", api.NovoLembrete{Texto: "ligar", Quando: time.Now().Add(time.Hour), Organizacao: "Exemplo"})

	esperar(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(recebidas) == 1 })
	mu.Lock()
	n := recebidas[0]
	mu.Unlock()
	if n.Evento.Tipo != "lembrete_criado" || n.Lembrete == nil || n.Lembrete.Texto != "ligar" || cabecalho != "Bearer x" {
		t.Fatalf("notificação inesperada: %+v (cabeçalho %q)", n, cabecalho)
	}
	esperar(t, func() bool {
		conteudo, _ := os.ReadFile(saida)
		var x api.Notificacao
		return json.Unmarshal(bytes.SplitN(conteudo, []byte("\n"), 2)[0], &x) == nil && x.Tarefa != nil && x.Tarefa.Id == "T-0002" &&
			bytes.HasSuffix(bytes.TrimSpace(conteudo), []byte("T-0002"))
	})
	esperar(t, func() bool { c, _, _ := s.Cursor("nuvem"); u, _ := s.UltimoEvento(); return c == u })
}

func TestGanchoQueDesisteRegistraAFalha(t *testing.T) {
	s, api_ := novoServidor(t)
	i, err := Nova(Config{Ganchos: []Gancho{{Nome: "quebrado", Eventos: []string{"tarefa_*"}, Comando: []string{"false"}}}}, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	i.intervalo = 10 * time.Millisecond
	i.tentativas = []time.Duration{time.Millisecond}
	ctx, cancelar := context.WithCancel(context.Background())
	defer cancelar()
	i.Conectar(ctx, s)
	time.Sleep(50 * time.Millisecond)
	postar(t, api_.URL+"/tarefas", api.NovaTarefa{Titulo: "x", Organizacao: "Exemplo"})
	esperar(t, func() bool {
		eventos, _ := s.EventosDepois(0, 100)
		return len(eventos) == 2 && eventos[1].Tipo == "integracao_falhou" && eventos[1].Entidade == "gancho:quebrado"
	})
}

func TestTurnos(t *testing.T) {
	i, err := Nova(Config{Turnos: &Turnos{Manha: "07:30", Noite: "19:00"}}, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if got, quer := i.Opcoes().Turnos, (servidor.Turnos{Manha: 7*60 + 30, Almoco: 12 * 60, Tarde: 13 * 60, Noite: 19 * 60}); got != quer {
		t.Fatalf("turnos: %+v, esperava %+v", got, quer)
	}
}

func TestConfiguracaoInvalida(t *testing.T) {
	semAmbiente := func(string) string { return "" }
	for _, cfg := range []Config{
		{Fuso: "Lugar/Nenhum"},
		{Turnos: &Turnos{Tarde: "1h"}},
		{Turnos: &Turnos{Tarde: "07:00"}},
		{Turnos: &Turnos{Almoco: "13:30"}},
		{Ganchos: []Gancho{{Nome: "a", Eventos: []string{"x"}}}},
		{Ganchos: []Gancho{{Nome: "a", Comando: []string{"true"}}}},
		{Ganchos: []Gancho{{Nome: "a", Eventos: []string{"x"}, Comando: []string{"true"}}, {Nome: "a", Eventos: []string{"x"}, Comando: []string{"true"}}}},
	} {
		if _, err := Nova(cfg, semAmbiente); err == nil {
			t.Errorf("configuração aceita: %+v", cfg)
		}
	}
}
