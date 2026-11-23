package ferramentas

import (
	"log"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	rotas "github.com/rpagliuca/rotas-e-comercios-mcp/ferramentas"
	"github.com/rpagliuca/rotas-e-comercios-mcp/google"
)

// Rotas, comércios, previsão do tempo e mapas pelo Google Maps Platform (repositório
// rotas-e-comercios-mcp). A chave e a conta de serviço ficam em ~/.config/rotas-e-comercios-mcp;
// sem a chave, o rex segue sem essas ferramentas.
var carregarRotas = sync.OnceValue(func() *rotas.Ferramentas {
	g, err := google.NovoDoPadrao()
	if err != nil {
		log.Printf("rotas e comércios desligadas: %v", err)
		return nil
	}
	return rotas.Novo(g)
})

// comRotas junta as instruções das ferramentas de rotas, se elas estiverem ligadas.
func comRotas(instrucoes string) string {
	if carregarRotas() == nil {
		return instrucoes
	}
	return instrucoes + " " + rotas.Instrucoes
}

func registrarRotas(s *mcp.Server) {
	if r := carregarRotas(); r != nil {
		r.Registrar(s)
	}
}
