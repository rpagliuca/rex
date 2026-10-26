// Comando rex.
//
//	rex servir   servidor da API (único processo que escreve no banco)
//	rex mcp      servidor MCP por stdio, para um agente de IA, falando com a API
package main

import (
	"compress/gzip"
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rpagliuca/rex/internal/banco"
	"github.com/rpagliuca/rex/internal/ferramentas"
	"github.com/rpagliuca/rex/internal/integracao"
	"github.com/rpagliuca/rex/internal/lancador"
	"github.com/rpagliuca/rex/internal/login"
	"github.com/rpagliuca/rex/internal/servidor"
	"github.com/rpagliuca/rex/web"
)

const enderecoPadrao = "127.0.0.1:8787"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "uso: rex servir | mcp [opções]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "servir":
		servir(os.Args[2:])
	case "mcp":
		servirMCP(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "comando desconhecido: %q (use servir ou mcp)\n", os.Args[1])
		os.Exit(2)
	}
}

func servir(args []string) {
	opcoes := flag.NewFlagSet("servir", flag.ExitOnError)
	caminho := opcoes.String("banco", "rex.db", "arquivo do SQLite")
	endereco := opcoes.String("endereco", enderecoPadrao, "endereços HTTP separados por vírgula (ex.: o local e o IP do Tailscale)")
	configLancador := opcoes.String("lancador", "", "arquivo JSON do lançador de agentes (opcional)")
	configIntegracoes := opcoes.String("integracoes", "", "arquivo JSON das integrações: ganchos e fuso (opcional; ver docs/integracao.md)")
	enderecoComLogin := opcoes.String("endereco-com-login", "", "endereços HTTP separados por vírgula que exigem login pelo Google (o do navegador, atrás de HTTPS); exige -login")
	configLogin := opcoes.String("login", "", "arquivo JSON do login pelo Google (opcional; ver docs/login.md)")
	opcoes.Parse(args)

	db, err := banco.Abrir(*caminho)
	if err != nil {
		log.Fatalf("abrir banco: %v", err)
	}
	defer db.Close()

	s := servidor.Novo(db)
	if *configLancador != "" {
		l, err := lancador.Carregar(*configLancador)
		if err != nil {
			log.Fatalf("lançador: %v", err)
		}
		l.Conectar(s)
		log.Printf("lançador de agentes ativo (%s)", *configLancador)
	}
	ctx := context.Background()
	if *configIntegracoes != "" {
		i, err := integracao.Carregar(*configIntegracoes)
		if err != nil {
			log.Fatalf("integrações: %v", err)
		}
		i.Conectar(ctx, s)
		log.Printf("integrações ativas (%s)", *configIntegracoes)
	}
	go integracao.Agendar(ctx, s, 15*time.Second)

	handler := rotas(s.Handler())
	ouvir := map[string]http.Handler{}
	for _, e := range strings.Split(*endereco, ",") {
		ouvir[strings.TrimSpace(e)] = comGzip(handler)
	}
	if (*enderecoComLogin == "") != (*configLogin == "") {
		log.Fatal("-endereco-com-login e -login andam juntos")
	}
	if *configLogin != "" {
		l, err := login.Carregar(*configLogin)
		if err != nil {
			log.Fatalf("login: %v", err)
		}
		// Conector personalizado do Claude: /mcp atrás de OAuth, só nos endereços com login. O túnel
		// entrega em loopback com o Host público, que a proteção contra DNS rebinding do SDK recusaria;
		// ela é dispensável aqui porque /mcp só aceita Bearer token, nunca cookie.
		conector := ferramentas.NovoEmProcesso(handler).ServidorDoConector()
		l.ComConector(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return conector },
			&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true}))
		for _, e := range strings.Split(*enderecoComLogin, ",") {
			ouvir[strings.TrimSpace(e)] = comGzip(l.Proteger(handler))
		}
		log.Printf("login pelo Google ativo (%s)", *configLogin)
	}
	falhou := make(chan error, len(ouvir))
	for e, h := range ouvir {
		log.Printf("rex ouvindo em http://%s (banco %s)", e, *caminho)
		go func() { falhou <- http.ListenAndServe(e, h) }()
	}
	log.Fatal(<-falhou)
}

// rotas junta a API e o frontend no mesmo endereço: os recursos da API têm caminho próprio e o
// resto é o frontend, que navega pelo hash da URL.
func rotas(apiHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	for _, recurso := range []string{"/tarefas", "/sessoes", "/eventos", "/importacao", "/lancador", "/origens", "/requisitos", "/templates", "/organizacoes", "/componentes", "/busca", "/arvore", "/lembretes", "/integracao", "/voz"} {
		mux.Handle(recurso, apiHandler)
		mux.Handle(recurso+"/", apiHandler)
	}
	mux.Handle("/", web.Handler())
	return mux
}

func servirMCP(args []string) {
	opcoes := flag.NewFlagSet("mcp", flag.ExitOnError)
	endereco := opcoes.String("api", "http://"+enderecoPadrao, "endereço da API do rex")
	journals := opcoes.String("journals", "", "raiz dos journals: cada sessão ganha <raiz>/<S-0001>/journal.md (opcional)")
	opcoes.Parse(args)

	f, err := ferramentas.Novo(*endereco)
	if err != nil {
		log.Fatalf("cliente da API: %v", err)
	}
	f.ComJournals(*journals)
	// stdout é o canal do protocolo; logs vão para stderr.
	log.SetOutput(os.Stderr)
	if err := f.Servidor().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

// comGzip comprime as respostas para quem aceita gzip: listas JSON e o JavaScript do frontend
// caem a uma fração do tamanho, o que pesa no celular.
func comGzip(proximo http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			proximo.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		z := gzip.NewWriter(w)
		defer z.Close()
		proximo.ServeHTTP(&escritorGzip{ResponseWriter: w, z: z}, r)
	})
}

type escritorGzip struct {
	http.ResponseWriter
	z *gzip.Writer
}

func (e *escritorGzip) Write(b []byte) (int, error) { return e.z.Write(b) }

// WriteHeader descarta o Content-Length calculado sobre o corpo sem compressão.
func (e *escritorGzip) WriteHeader(status int) {
	e.Header().Del("Content-Length")
	e.ResponseWriter.WriteHeader(status)
}
