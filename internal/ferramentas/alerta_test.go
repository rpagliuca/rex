package ferramentas

import (
	"strings"
	"testing"
	"time"

	"github.com/rpagliuca/rex/api"
)

// TestAlertaNoturno: só o grau 3 de precisão hora entre 23h e 7h, no fuso do lembrete, gera alerta.
func TestAlertaNoturno(t *testing.T) {
	fuso := time.FixedZone("-03", -3*3600)
	casos := []struct {
		hora, grau int
		precisao   api.PrecisaoLembrete
		alerta     bool
	}{
		{1, 3, api.PrecisaoHora, true},
		{23, 3, api.PrecisaoHora, true},
		{6, 3, api.PrecisaoHora, true},
		{7, 3, api.PrecisaoHora, false},
		{22, 3, api.PrecisaoHora, false},
		{1, 2, api.PrecisaoHora, false},
		{1, 3, api.PrecisaoDia, false},
	}
	for _, c := range casos {
		x := api.Lembrete{Id: "L-0001", Quando: time.Date(2026, 11, 17, c.hora, 36, 0, 0, fuso), Precisao: c.precisao, AvisoMax: c.grau}
		a := alertaNoturno(x)
		if (a != "") != c.alerta {
			t.Errorf("%02d:36 grau %d %s: alerta %q", c.hora, c.grau, c.precisao, a)
		}
		if c.hora == 1 && c.alerta && !strings.Contains(a, "01:31") {
			t.Errorf("alarme 5 min antes: %q", a)
		}
	}
}
