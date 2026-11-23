package ferramentas

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	rotas "github.com/rpagliuca/rotas-e-comercios-mcp/ferramentas"
	"github.com/rpagliuca/rotas-e-comercios-mcp/google"
)

// Os testes não dependem da chave do Google desta máquina.
func init() { carregarRotas = func() *rotas.Ferramentas { return nil } }

func TestConectorComRotas(t *testing.T) {
	antes := carregarRotas
	carregarRotas = func() *rotas.Ferramentas { return rotas.Novo(google.Novo("chave-teste")) }
	t.Cleanup(func() { carregarRotas = antes })
	ctx := context.Background()
	for nome, s := range map[string]*mcp.Server{
		"conector": NovoEmProcesso(nil).ServidorDoConector(),
		"local":    NovoEmProcesso(nil).Servidor(),
	} {
		ladoServidor, ladoCliente := mcp.NewInMemoryTransports()
		if _, err := s.Connect(ctx, ladoServidor, nil); err != nil {
			t.Fatal(err)
		}
		sessao, err := mcp.NewClient(&mcp.Implementation{Name: "teste", Version: "0"}, nil).Connect(ctx, ladoCliente, nil)
		if err != nil {
			t.Fatal(err)
		}
		lista, err := sessao.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		tem := map[string]bool{}
		for _, f := range lista.Tools {
			tem[f.Name] = true
		}
		for _, n := range []string{"rota", "planejar_visitas", "buscar_comercios", "previsao_do_tempo", "mapa"} {
			if !tem[n] {
				t.Errorf("%s sem %s", nome, n)
			}
		}
		if !strings.Contains(sessao.InitializeResult().Instructions, "planejar_visitas") {
			t.Errorf("%s sem as instruções de rotas", nome)
		}
		sessao.Close()
	}
}
