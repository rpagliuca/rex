package servidor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rpagliuca/rex/api"
	"github.com/rpagliuca/rex/internal/voz"
)

type vozFalsa struct {
	mu       sync.Mutex
	tela     string
	semTmux  bool
	escritos []string
	teclas   []string
	conversa string
}

func (v *vozFalsa) Tela(api.Sessao) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.semTmux {
		return "", os.ErrNotExist
	}
	return v.tela, nil
}
func (v *vozFalsa) Escrever(_ api.Sessao, texto string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.escritos = append(v.escritos, texto)
	return nil
}
func (v *vozFalsa) Teclar(_ api.Sessao, tecla string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.teclas = append(v.teclas, tecla)
	return nil
}
func (v *vozFalsa) Conversa(api.Sessao) string { return v.conversa }
func (v *vozFalsa) mudar(f func()) {
	v.mu.Lock()
	defer v.mu.Unlock()
	f()
}

const (
	telaOciosa      = "✻ done\n❯ \n  ⏵⏵ bypass permissions on\n"
	telaTrabalhando = "✢ Thinking…\n❯ \n  ⏵⏵ bypass permissions on · esc to interrupt\n"
	telaPermissao   = " Do you want to proceed?\n ❯ 1. Yes\n   2. No\n Esc to cancel\n"
)

func TestVozPorSessao(t *testing.T) {
	a := novoAmbiente(t)
	esperaDaTecla, intervaloDaEspera = 0, 10*time.Millisecond
	v := &vozFalsa{tela: telaOciosa, conversa: filepath.Join(t.TempDir(), "c.jsonl")}
	a.servidor.ComVoz(v)

	tarefa := a.tarefa("Deploy")
	semSessao := a.tarefa("Sem agente")
	a.chamar("POST", "/voz/sessoes/"+semSessao.Id+"/mensagens", "", api.MensagemDeVoz{Texto: "oi"}, 409, nil)
	a.chamar("GET", "/voz/sessoes/S-0099", "", nil, 404, nil)
	s := a.sessao("Claude Opus 5.5 Medium", tarefa.Id)

	var estado api.Voz
	a.chamar("GET", "/voz/sessoes/"+strings.ToLower(tarefa.Id), "", nil, 200, &estado)
	if estado.Sessao != s.Sessao.Id || estado.Estado != api.VozOcioso {
		t.Fatalf("pela tarefa: %+v", estado)
	}

	var pedido api.PedidoDeVoz
	caminho := "/voz/sessoes/" + s.Sessao.Id
	a.chamar("POST", caminho+"/mensagens", "", api.MensagemDeVoz{Texto: "como está\n o deploy?"}, 201, &pedido)
	a.chamar("POST", caminho+"/mensagens", "", api.MensagemDeVoz{Texto: "e o teste?"}, 201, &pedido)
	if pedido.Pedido != 2 || len(v.escritos) != 2 || !strings.HasPrefix(v.escritos[0], voz.Prefixo+"1] como está o deploy?") {
		t.Fatalf("pedidos: %+v %q", pedido, v.escritos)
	}

	// Tela de permissão: a frase cairia na escolha; a tecla responde.
	v.tela = telaPermissao
	a.chamar("POST", caminho+"/mensagens", "", api.MensagemDeVoz{Texto: "oi"}, 409, nil)
	a.chamar("GET", caminho, "", nil, 200, &estado)
	if estado.Estado != api.VozPedindoPermissao || estado.Tela == nil || !strings.Contains(*estado.Tela, "1. Yes") {
		t.Fatalf("permissão: %+v", estado)
	}
	a.chamar("POST", caminho+"/teclas", "", api.TeclaDeVoz{Tecla: "1"}, 200, nil)
	v.tela = telaOciosa
	a.chamar("POST", caminho+"/teclas", "", api.TeclaDeVoz{Tecla: "1"}, 409, nil)
	a.chamar("POST", caminho+"/teclas", "", api.TeclaDeVoz{Tecla: "x"}, 400, nil)
	a.chamar("POST", caminho+"/teclas", "", api.TeclaDeVoz{Tecla: api.TeclaEscape}, 200, nil)
	if strings.Join(v.teclas, ",") != "1,Escape" {
		t.Fatalf("teclas: %v", v.teclas)
	}

	// Ponteiro: ler sem ouvir não anda. O sufixo de quando era o atalho da Siri também sai.
	linhas := `{"type":"user","uuid":"u1","timestamp":"2026-10-22T10:00:00Z","message":{"content":"[rex voz · pedido 1] como está o deploy?` + sufixosDaVoz[1] + `"}}
{"type":"assistant","uuid":"u2","timestamp":"2026-10-22T10:00:09Z","message":{"content":[{"type":"text","text":"Terminou."}]}}
`
	os.WriteFile(v.conversa, []byte(linhas), 0o644)
	a.chamar("GET", caminho, "", nil, 200, &estado)
	if len(estado.Novidades) != 2 || estado.Novidades[0].Texto != "como está o deploy?" || *estado.Novidades[1].Pedido != 1 {
		t.Fatalf("novidades: %+v", estado.Novidades)
	}
	a.chamar("GET", caminho, "", nil, 200, &estado)
	if len(estado.Novidades) != 2 {
		t.Fatalf("sem ouvir, continuam novas: %+v", estado.Novidades)
	}

	// ouvir (conector do app): viram ouvidas na hora.
	linhas += `{"type":"assistant","uuid":"u3","timestamp":"2026-10-22T10:01:00Z","message":{"content":[{"type":"text","text":"E os testes passaram."}]}}
`
	os.WriteFile(v.conversa, []byte(linhas), 0o644)
	a.chamar("GET", caminho+"?ouvir=true", "", nil, 200, &estado)
	if len(estado.Novidades) != 3 {
		t.Fatalf("com ouvir: %+v", estado.Novidades)
	}
	a.chamar("GET", caminho, "", nil, 200, &estado)
	if len(estado.Novidades) != 0 {
		t.Fatalf("ouvir devia marcar na hora: %+v", estado.Novidades)
	}

	// esperar: com o agente trabalhando e calado, segura até o limite.
	v.tela = telaTrabalhando
	inicio := time.Now()
	a.chamar("GET", caminho+"?esperar=1", "", nil, 200, &estado)
	if time.Since(inicio) < time.Second || estado.Estado != api.VozTrabalhando {
		t.Fatalf("não esperou: %v %+v", time.Since(inicio), estado)
	}

	v.semTmux = true
	a.chamar("GET", caminho, "", nil, 200, &estado)
	if estado.Estado != api.VozSemAgente {
		t.Fatalf("sem tmux: %+v", estado)
	}
	a.chamar("POST", caminho+"/mensagens", "", api.MensagemDeVoz{Texto: "oi"}, 409, nil)
}

func TestVozPanorama(t *testing.T) {
	a := novoAmbiente(t)
	v := &vozFalsa{tela: telaOciosa, conversa: filepath.Join(t.TempDir(), "c.jsonl")}
	a.servidor.ComVoz(v)
	deploy := a.tarefa("Deploy do cartório")
	s := a.sessao("Claude Opus 5.5 Medium", deploy.Id)
	a.chamar("POST", "/sessoes/"+s.Sessao.Id+"/espera", s.Token, api.Espera{Mensagem: "aprovar o PR"}, 200, nil)
	os.WriteFile(v.conversa, []byte(`{"type":"assistant","uuid":"u0","timestamp":"2026-10-22T09:00:00Z","message":{"content":[{"type":"text","text":"Começando."}]}}
{"type":"user","uuid":"u1","timestamp":"2026-10-22T10:00:00Z","message":{"content":"abre o PR"}}
{"type":"assistant","uuid":"u2","timestamp":"2026-10-22T10:00:09Z","message":{"content":[{"type":"text","text":"PR aberto."}]}}
`), 0o644)

	var panorama []api.PanoramaDeVoz
	a.chamar("GET", "/voz/sessoes", "", nil, 200, &panorama)
	if len(panorama) != 1 {
		t.Fatalf("panorama: %+v", panorama)
	}
	p := panorama[0]
	if p.Sessao != s.Sessao.Id || p.Aguardando == nil || *p.Aguardando != "aprovar o PR" || p.Estado != api.VozOcioso ||
		p.NaoOuvidas != 1 || p.Ultima == nil || *p.Ultima != "PR aberto." || (*p.Titulos)[0] != "Deploy do cartório" {
		t.Fatalf("panorama: %+v", p)
	}

	// Leve: palavras do nome, só o começo de um texto longo e nada da tela, nem pedindo permissão.
	a.chamar("PATCH", "/sessoes/"+s.Sessao.Id, s.Token, api.AlteracaoSessao{Nome: ptr("deploy-cartorio")}, 200, nil)
	longo := strings.Repeat("palavra ", 20) + "fim. " + strings.Repeat("resto ", 300)
	linha, _ := json.Marshal(map[string]any{"type": "assistant", "uuid": "u3", "timestamp": "2026-10-22T10:01:00Z",
		"message": map[string]any{"content": []map[string]string{{"type": "text", "text": longo}}}})
	f, _ := os.OpenFile(v.conversa, os.O_APPEND|os.O_WRONLY, 0o644)
	f.Write(append(linha, '\n'))
	f.Close()
	v.tela = telaPermissao
	a.chamar("GET", "/voz/sessoes", "", nil, 200, &panorama)
	p = panorama[0]
	if strings.Join(*p.PalavrasChave, ",") != "deploy,cartorio" || p.Estado != api.VozPedindoPermissao ||
		*p.Ultima != strings.Repeat("palavra ", 20)+"fim." {
		t.Fatalf("panorama leve: %+v %q", p, *p.Ultima)
	}
}

func TestMensagemEntreSessoes(t *testing.T) {
	a := novoAmbiente(t)
	v := &vozFalsa{tela: telaTrabalhando, conversa: filepath.Join(t.TempDir(), "c.jsonl")}
	a.servidor.ComVoz(v)
	global := a.sessao("Claude Opus 5.5 Medium")
	a.chamar("PATCH", "/sessoes/"+global.Sessao.Id, global.Token, api.AlteracaoSessao{Nome: ptr("agente-global")}, 200, nil)
	planilha := a.sessao("Claude Opus 5.5 Medium", a.tarefa("Planilha").Id)
	caminho := "/sessoes/" + planilha.Sessao.Id + "/mensagens"

	// A origem é a sessão do token: sem token, não há a quem responder.
	a.chamar("POST", caminho, "", api.MensagemDeVoz{Texto: "oi"}, 401, nil)
	a.chamar("POST", caminho, global.Token, api.MensagemDeVoz{Texto: " "}, 400, nil)
	a.chamar("POST", "/sessoes/"+global.Sessao.Id+"/mensagens", global.Token, api.MensagemDeVoz{Texto: "oi"}, 400, nil)

	var m api.MensagemEntreSessoes
	a.chamar("POST", caminho, global.Token, api.MensagemDeVoz{Texto: "posso desligar\n a cópia 1?"}, 201, &m)
	if m.De != global.Sessao.Id || m.Para != planilha.Sessao.Id || m.Estado != api.VozTrabalhando || len(v.escritos) != 1 {
		t.Fatalf("mensagem: %+v %q", m, v.escritos)
	}
	escrito := v.escritos[0]
	if escrito != voz.MensagemDeSessao(global.Sessao.Id, "agente-global", "posso desligar a cópia 1?") {
		t.Fatalf("escrito: %q", escrito)
	}

	// A resposta volta pela mesma rota, para a origem.
	a.chamar("POST", "/sessoes/"+global.Sessao.Id+"/mensagens", planilha.Token, api.MensagemDeVoz{Texto: "pode"}, 201, &m)
	if m.De != planilha.Sessao.Id || !strings.HasPrefix(v.escritos[1], voz.PrefixoDeSessao+planilha.Sessao.Id+"] pode") {
		t.Fatalf("resposta: %+v %q", m, v.escritos[1])
	}

	// No rex voz, a mensagem aparece como da sessão, não do Rafael.
	conversa, _ := json.Marshal(map[string]any{"type": "attachment", "uuid": "x1", "timestamp": "2026-10-23T17:27:19Z",
		"attachment": map[string]any{"type": "queued_command", "prompt": escrito}})
	os.WriteFile(v.conversa, append(conversa, '\n'), 0o644)
	var estado api.Voz
	a.chamar("GET", "/voz/sessoes/"+planilha.Sessao.Id, "", nil, 200, &estado)
	if len(estado.Novidades) != 1 || estado.Novidades[0].De != api.DaSessao || *estado.Novidades[0].Origem != global.Sessao.Id ||
		estado.Novidades[0].Texto != "posso desligar a cópia 1?" {
		t.Fatalf("novidades: %+v", estado.Novidades)
	}

	v.tela = telaPermissao
	a.chamar("POST", caminho, global.Token, api.MensagemDeVoz{Texto: "oi"}, 409, nil)
}
