package lancador

import (
	"errors"
	"fmt"
	"time"

	"github.com/rpagliuca/rex/api"
	"github.com/rpagliuca/rex/internal/voz"
)

// O rex voz fala com o agente de uma sessão pelo tmux dele: lê a tela, escreve a frase do Rafael
// e aperta teclas, como o Rafael faria no laptop.

// ErrSemAgente é a sessão ativa cujo tmux não foi achado.
var ErrSemAgente = errors.New("o tmux do agente desta sessão não foi achado")

// painelDaSessao acha o tmux do agente: pelo chat (agente iniciado à mão também) e, sem ele,
// pela tarefa do ambiente (agente subido pelo lançador).
func (l *Lancador) painelDaSessao(x api.Sessao) string {
	if x.Chat != nil && *x.Chat != "" {
		if nome := l.painelDoChat(*x.Chat); nome != "" {
			return nome
		}
	}
	for _, t := range x.Tarefas {
		if nome, err := l.encontrar(t); err == nil && nome != "" {
			return nome
		}
	}
	return ""
}

// ConferirOrfa devolve nil só quando prova que o agente da sessão não está vivo. Na dúvida, recusa:
// encerrar a sessão de um agente vivo tira dele a tarefa sem ele saber. Painel não basta, porque um
// chat aberto fora do tmux não tem painel; por isso os processos do chat contam também, mesmo um que
// o agente tenha soltado e deixado para trás.
func (l *Lancador) ConferirOrfa(x api.Sessao) error {
	switch {
	case x.TipoAgente != api.Ia:
		return errors.New("é a sessão de um humano, que a encerra pelo frontend")
	case x.Servidor != l.maquina:
		return fmt.Errorf("ela roda em %s, e daqui só dá para conferir %s", x.Servidor, l.maquina)
	case x.Chat == nil || *x.Chat == "":
		return errors.New("ela não registrou o chat, então não dá para procurar o agente")
	}
	if nome := l.painelDaSessao(x); nome != "" {
		return fmt.Errorf("o agente dela está no tmux %s", nome)
	}
	if pids := l.processosDoChat(*x.Chat); len(pids) > 0 {
		return fmt.Errorf("ainda há processos do chat dela, fora do tmux (pids %v)", pids)
	}
	return nil
}

// Tela devolve o que a tela do agente mostra agora.
func (l *Lancador) Tela(x api.Sessao) (string, error) {
	nome := l.painelDaSessao(x)
	if nome == "" {
		return "", ErrSemAgente
	}
	return l.executar("capture-pane", "-p", "-t", "="+nome+":")
}

// Escrever digita o texto no agente e manda; com um turno em andamento, o Claude Code o põe na fila.
func (l *Lancador) Escrever(x api.Sessao, texto string) error {
	nome := l.painelDaSessao(x)
	if nome == "" {
		return ErrSemAgente
	}
	if _, err := l.executar("send-keys", "-t", "="+nome+":", "-l", texto); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond) // o Enter separado do texto evita que vire quebra de linha
	_, err := l.executar("send-keys", "-t", "="+nome+":", "Enter")
	return err
}

// Teclar aperta uma tecla (nome do tmux: "1", "Enter", "Escape") no agente.
func (l *Lancador) Teclar(x api.Sessao, tecla string) error {
	nome := l.painelDaSessao(x)
	if nome == "" {
		return ErrSemAgente
	}
	_, err := l.executar("send-keys", "-t", "="+nome+":", tecla)
	return err
}

// Conversa devolve o transcript do agente da sessão, ou "".
func (l *Lancador) Conversa(x api.Sessao) string {
	if x.Chat == nil {
		return ""
	}
	return voz.Arquivo(l.cfg.Projetos, *x.Chat)
}
