package voz

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const transcript = `{"type":"user","uuid":"a","timestamp":"2026-10-22T10:00:00Z","message":{"content":"comece pela tarefa"}}
{"type":"assistant","uuid":"b","timestamp":"2026-10-22T10:00:05Z","message":{"content":[{"type":"thinking","thinking":"","signature":"x"},{"type":"text","text":"Comecei."}]}}
{"type":"user","uuid":"c","timestamp":"2026-10-22T10:00:06Z","message":{"content":[{"type":"tool_result","content":"x"}]}}
{"type":"attachment","uuid":"c2","timestamp":"2026-10-22T10:00:06Z"}
{"type":"user","uuid":"d","timestamp":"2026-10-22T10:01:00Z","message":{"content":"[rex voz · pedido 1] como está o deploy?"}}
{"type":"user","uuid":"d2","isMeta":true,"timestamp":"2026-10-22T10:01:00Z","message":{"content":"meta"}}
{"type":"user","uuid":"d3","timestamp":"2026-10-22T10:01:00Z","message":{"content":"<command-name>/rename</command-name>"}}
{"type":"assistant","uuid":"e","isSidechain":true,"timestamp":"2026-10-22T10:01:02Z","message":{"content":[{"type":"text","text":"subagente"}]}}
{"type":"attachment","uuid":"e2","timestamp":"2026-10-22T10:01:05Z","attachment":{"type":"queued_command","prompt":"[rex voz · pedido 2] e o teste?"}}
{"type":"attachment","uuid":"e3","timestamp":"2026-10-22T10:01:05Z","attachment":{"type":"total_tokens_reminder"}}
{"type":"assistant","uuid":"f","timestamp":"2026-10-22T10:01:09Z","message":{"content":[{"type":"text","text":"O deploy terminou."}]}}
{"type":"last-prompt"}
`

func escrever(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "-home-usuario")
	os.MkdirAll(dir, 0o755)
	caminho := filepath.Join(dir, "chat-1.jsonl")
	if err := os.WriteFile(caminho, []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	return caminho
}

func TestLerDepoisDoPonteiro(t *testing.T) {
	caminho := escrever(t)
	if Arquivo(filepath.Dir(filepath.Dir(caminho)), "chat-1") != caminho || Arquivo(filepath.Dir(caminho), "../x") != "" {
		t.Fatal("Arquivo não achou o transcript")
	}
	l, err := Ler(caminho, "b")
	if err != nil {
		t.Fatal(err)
	}
	if l.Ate != "f" || l.Omitidas != 0 || len(l.Novidades) != 3 {
		t.Fatalf("leitura inesperada: %+v", l)
	}
	if n := l.Novidades[0]; !n.DoUsuario || n.Pedido != 1 || n.Texto != "como está o deploy?" {
		t.Fatalf("frase do usuário: %+v", n)
	}
	if n := l.Novidades[1]; !n.DoUsuario || n.Pedido != 2 || n.Texto != "e o teste?" {
		t.Fatalf("frase mandada no meio do turno: %+v", n)
	}
	if n := l.Novidades[2]; n.DoUsuario || n.Pedido != 2 || n.Texto != "O deploy terminou." {
		t.Fatalf("resposta do agente: %+v", n)
	}
	// Tudo ouvido: nada novo.
	if l, _ := Ler(caminho, "f"); len(l.Novidades) != 0 {
		t.Fatalf("depois do fim: %+v", l)
	}
}

func TestLerSemPonteiroTrazSoAsUltimas(t *testing.T) {
	caminho := escrever(t)
	for _, ponteiro := range []string{"", "sumiu"} {
		l, err := Ler(caminho, ponteiro)
		if err != nil {
			t.Fatal(err)
		}
		if len(l.Novidades) != semPonteiro || l.Omitidas != 2 || l.Novidades[0].Texto != "como está o deploy?" {
			t.Fatalf("ponteiro %q: %+v", ponteiro, l)
		}
	}
}

func TestEncurtarGuardaComecoEFim(t *testing.T) {
	texto := strings.Repeat("a", 2000) + "FIM"
	if e := encurtar(texto); !strings.HasSuffix(e, "FIM") || len([]rune(e)) > TamanhoDoTexto+10 {
		t.Fatalf("encurtado errado: %d", len(e))
	}
}

func TestLerTela(t *testing.T) {
	casos := []struct {
		tela   string
		estado Estado
	}{
		{"✢ Slithering… (55s)\n❯ \n  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt\n", Trabalhando},
		{"✻ Cogitated for 2s · done 8:18 AM\n❯ pode mergear\n  ⏵⏵ bypass permissions on (shift+tab to cycle)\n", Ocioso},
		{"● Bash(rm -rf build)\n Do you want to proceed?\n ❯ 1. Yes\n   2. No, and tell Claude what to do differently (esc)\n\n Esc to cancel\n", PedindoPermissao},
		{"❯ 1A, 2B\n  ⏵⏵ bypass permissions on\n", Ocioso},
	}
	for _, c := range casos {
		estado, fim := LerTela(c.tela)
		if estado != c.estado {
			t.Errorf("%q: %s, esperado %s", c.tela, estado, c.estado)
		}
		if (estado == PedindoPermissao) != (fim != "") {
			t.Errorf("%q: tela devolvida %q", c.tela, fim)
		}
	}
}

// Caso da S-0256 (2026-10-23): a resposta a um pedido, dada no meio de um turno que segue com
// ferramentas, fica gravada como thinking com texto (narração), não como text.
const narracao = `{"type":"assistant","uuid":"a","timestamp":"2026-10-23T17:25:34Z","message":{"content":[{"type":"tool_use","name":"Bash"}]}}
{"type":"attachment","uuid":"b","timestamp":"2026-10-23T17:27:19Z","attachment":{"type":"queued_command","prompt":"[rex · de S-0254 agente-global] posso desligar a cópia 1? — (mensagem da sessão S-0254 agente-global, não do usuário: responda com a ferramenta enviar_a_sessao do rex para S-0254; a resposta entra no chat dela)"}}
{"type":"assistant","uuid":"c","timestamp":"2026-10-23T17:29:03Z","message":{"content":[{"type":"thinking","thinking":"","signature":"y"}]}}
{"type":"assistant","uuid":"d","timestamp":"2026-10-23T17:29:03Z","message":{"content":[{"type":"thinking","thinking":"Não dependo da cópia 1: pode desligar.\n\n","signature":"z"}]}}
{"type":"assistant","uuid":"e","timestamp":"2026-10-23T17:29:03Z","message":{"content":[{"type":"tool_use","name":"Bash"}]}}
`

func TestLerNarracaoEMensagemDeSessao(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "chat.jsonl")
	if err := os.WriteFile(caminho, []byte(narracao), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := Ler(caminho, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Novidades) != 2 {
		t.Fatalf("novidades: %+v", l.Novidades)
	}
	if n := l.Novidades[0]; n.DoUsuario || n.Origem != "S-0254" || n.Texto != "posso desligar a cópia 1?" {
		t.Fatalf("mensagem da outra sessão: %+v", n)
	}
	if n := l.Novidades[1]; n.DoUsuario || n.Origem != "" || n.Texto != "Não dependo da cópia 1: pode desligar." {
		t.Fatalf("narração do agente: %+v", n)
	}
	if l.Ultima == nil || l.Ultima.Texto != "Não dependo da cópia 1: pode desligar." || l.Respostas != 1 {
		t.Fatalf("última e respostas: %+v", l)
	}
}

func TestMensagemDeSessaoSeDesmonta(t *testing.T) {
	m := MensagemDeSessao("S-0254", "agente-global", "posso desligar a cópia 1?")
	if !strings.HasPrefix(m, "[rex · de S-0254 agente-global] posso") || strings.Contains(m, "\n") ||
		!strings.Contains(m, "enviar_a_sessao do rex para S-0254") || m[len(m)-1] != ')' {
		t.Fatalf("mensagem: %q", m)
	}
	if sessaoNoTexto.FindStringSubmatch(MensagemDeSessao("S-0001", "", "oi"))[1] != "S-0001" {
		t.Fatal("sem nome não se desmonta")
	}
}
