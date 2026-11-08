package servidor

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rpagliuca/rex/api"
)

func idOrigem(n int64) string    { return idCom("O", n) }
func idRequisito(n int64) string { return idCom("R", n) }

// ---- Origens ----

func lerOrigem(linha varredor) (api.Origem, error) {
	var o api.Origem
	var n int64
	var tipo, criada string
	if err := linha.Scan(&n, &o.Nome, &tipo, &o.Descricao, &criada); err != nil {
		return o, err
	}
	o.Id, o.Tipo, o.CriadaEm = idOrigem(n), api.TipoOrigem(tipo), lerMomento(criada)
	return o, nil
}

func carregarOrigem(c consultor, n int64) (api.Origem, error) {
	o, err := lerOrigem(c.QueryRow(`SELECT id, nome, tipo, descricao, criada_em FROM origens WHERE id = ?`, n))
	if errors.Is(err, sql.ErrNoRows) {
		return o, falha(http.StatusNotFound, "origem %s não encontrada", idOrigem(n))
	}
	return o, err
}

func tipoDeOrigemValido(t api.TipoOrigem) bool { return t == api.Pessoa || t == api.Documento }

// nomeEmUso traduz a violação de UNIQUE do nome numa mensagem clara.
func nomeEmUso(err error, nome string) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return falha(http.StatusConflict, "já existe uma origem chamada %q", nome)
	}
	return err
}

func (s *Servidor) ListarOrigens(w http.ResponseWriter, r *http.Request) {
	linhas, err := s.db.Query(`SELECT id, nome, tipo, descricao, criada_em FROM origens ORDER BY nome`)
	if err != nil {
		responderErro(w, err)
		return
	}
	defer linhas.Close()
	origens := []api.Origem{}
	for linhas.Next() {
		o, err := lerOrigem(linhas)
		if err != nil {
			responderErro(w, err)
			return
		}
		origens = append(origens, o)
	}
	if err := linhas.Err(); err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusOK, origens)
}

func (s *Servidor) CriarOrigem(w http.ResponseWriter, r *http.Request) {
	var nova api.NovaOrigem
	if err := ler(r, &nova); err != nil {
		responderErro(w, err)
		return
	}
	var criada api.Origem
	err := s.emTransacao(func(tx *sql.Tx) error {
		quem, err := autor(tx, r)
		if err != nil {
			return err
		}
		if vazio(nova.Nome) {
			return falha(http.StatusBadRequest, "o nome é obrigatório")
		}
		if !tipoDeOrigemValido(nova.Tipo) {
			return falha(http.StatusBadRequest, "tipo de origem inválido: %q (pessoa ou documento)", nova.Tipo)
		}
		descricao := ""
		if nova.Descricao != nil {
			descricao = *nova.Descricao
		}
		res, err := tx.Exec(`INSERT INTO origens (nome, tipo, descricao, criada_em) VALUES (?, ?, ?, ?)`,
			strings.TrimSpace(nova.Nome), string(nova.Tipo), descricao, s.momento())
		if err != nil {
			return nomeEmUso(err, nova.Nome)
		}
		n, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if criada, err = carregarOrigem(tx, n); err != nil {
			return err
		}
		return s.registrarEvento(tx, quem, criada.Id, "origem_criada", map[string]any{"nome": criada.Nome, "tipo": criada.Tipo})
	})
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusCreated, criada)
}

func (s *Servidor) ObterOrigem(w http.ResponseWriter, r *http.Request, id api.Id) {
	n, err := numero(id, "O")
	if err != nil {
		responderErro(w, err)
		return
	}
	o, err := carregarOrigem(s.db, n)
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusOK, o)
}

func (s *Servidor) AlterarOrigem(w http.ResponseWriter, r *http.Request, id api.Id) {
	n, err := numero(id, "O")
	if err != nil {
		responderErro(w, err)
		return
	}
	var alteracao api.AlteracaoOrigem
	if err := ler(r, &alteracao); err != nil {
		responderErro(w, err)
		return
	}
	var alterada api.Origem
	err = s.emTransacao(func(tx *sql.Tx) error {
		quem, err := autor(tx, r)
		if err != nil {
			return err
		}
		if _, err := carregarOrigem(tx, n); err != nil {
			return err
		}
		campos := map[string]any{}
		if alteracao.Nome != nil {
			if vazio(*alteracao.Nome) {
				return falha(http.StatusBadRequest, "o nome não pode ficar vazio")
			}
			campos["nome"] = strings.TrimSpace(*alteracao.Nome)
		}
		if alteracao.Tipo != nil {
			if !tipoDeOrigemValido(*alteracao.Tipo) {
				return falha(http.StatusBadRequest, "tipo de origem inválido: %q", *alteracao.Tipo)
			}
			campos["tipo"] = string(*alteracao.Tipo)
		}
		if alteracao.Descricao != nil {
			campos["descricao"] = *alteracao.Descricao
		}
		if len(campos) == 0 {
			return falha(http.StatusBadRequest, "nenhum campo para alterar")
		}
		for coluna, valor := range campos {
			if _, err := tx.Exec(`UPDATE origens SET `+coluna+` = ? WHERE id = ?`, valor, n); err != nil {
				return nomeEmUso(err, fmt.Sprint(campos["nome"]))
			}
		}
		if alterada, err = carregarOrigem(tx, n); err != nil {
			return err
		}
		return s.registrarEvento(tx, quem, alterada.Id, "origem_alterada", campos)
	})
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusOK, alterada)
}

// ---- Requisitos ----

// ligacoes devolve, para cada requisito, os ids de pais, filhos e tarefas, numa leitura só de cada tabela.
type ligacoes struct {
	pais, filhos, tarefas map[int64][]string
	// trabalho guarda, por requisito, status e última atualização das tarefas ligadas.
	trabalho map[int64][]tarefaLigada
	agora    time.Time
	// abertas: tarefas não concluídas de cada requisito; ultimas: a execução mais recente de cada um.
	abertas map[int64][]string
	ultimas map[int64]api.Execucao
	// filhosN: derivados por número; estados: memória do estado (herdado) de cada um.
	filhosN     map[int64][]int64
	estados     map[int64]api.EstadoDoRequisito
	componentes map[int64][]string
	// orgs: organização de cada requisito; zeros: os zeros atribuídos às organizações.
	orgs  map[int64]string
	zeros map[int64]bool
	// pontos: os de cada ligação filho → pai (ausente: não triada); pesos: memória do peso no total.
	pontos map[ligacao]int
	pesos  map[int64]float64
	// obsoletos: os requisitos marcados como obsoletos; não limitam os pais nem pesam entre os irmãos.
	obsoletos map[int64]bool
}

type ligacao struct{ filho, pai int64 }

type tarefaLigada struct {
	status     api.StatusTarefa
	atualizada time.Time
}

// MaxFilhos é a boa prática do rex: cada requisito se desdobra em no máximo cinco. Não é
// imposto; acima disso, o requisito pede atenção.
const MaxFilhos = 5

// diasParado é o tempo sem movimento a partir do qual um requisito só com tarefas paradas pede atenção.
const diasParado = 14

func carregarLigacoes(c consultor) (ligacoes, error) {
	l := ligacoes{pais: map[int64][]string{}, filhos: map[int64][]string{}, tarefas: map[int64][]string{},
		trabalho: map[int64][]tarefaLigada{}, agora: time.Now().UTC(),
		abertas: map[int64][]string{}, ultimas: map[int64]api.Execucao{},
		filhosN: map[int64][]int64{}, estados: map[int64]api.EstadoDoRequisito{},
		orgs: map[int64]string{}, zeros: map[int64]bool{}, componentes: map[int64][]string{},
		pontos: map[ligacao]int{}, pesos: map[int64]float64{}, obsoletos: map[int64]bool{}}
	if err := carregarOrganizacoesDosRequisitos(c, &l); err != nil {
		return l, err
	}
	if err := carregarComponentesDosRequisitos(c, &l); err != nil {
		return l, err
	}
	linhas, err := c.Query(`SELECT requisito_id, pai_id, pontos FROM derivacoes ORDER BY pai_id, requisito_id`)
	if err != nil {
		return l, err
	}
	for linhas.Next() {
		var filho, pai int64
		var pontos sql.NullInt64
		if err := linhas.Scan(&filho, &pai, &pontos); err != nil {
			linhas.Close()
			return l, err
		}
		if pontos.Valid {
			l.pontos[ligacao{filho, pai}] = int(pontos.Int64)
		}
		l.pais[filho] = append(l.pais[filho], idRequisito(pai))
		l.filhos[pai] = append(l.filhos[pai], idRequisito(filho))
		l.filhosN[pai] = append(l.filhosN[pai], filho)
	}
	if err := linhas.Close(); err != nil {
		return l, err
	}
	linhas, err = c.Query(`SELECT ` + colunasExecucao + ` FROM execucoes ORDER BY realizada_em, id`)
	if err != nil {
		return l, err
	}
	for linhas.Next() {
		e, n, err := lerExecucao(linhas)
		if err != nil {
			linhas.Close()
			return l, err
		}
		l.ultimas[n] = e
	}
	if err := linhas.Close(); err != nil {
		return l, err
	}
	linhas, err = c.Query(`SELECT tr.requisito_id, tr.tarefa_id, t.status, t.atualizada_em
		FROM tarefa_requisito tr JOIN tarefas t ON t.id = tr.tarefa_id ORDER BY tr.tarefa_id`)
	if err != nil {
		return l, err
	}
	defer linhas.Close()
	for linhas.Next() {
		var requisito, tarefa int64
		var status, atualizada string
		if err := linhas.Scan(&requisito, &tarefa, &status, &atualizada); err != nil {
			return l, err
		}
		l.tarefas[requisito] = append(l.tarefas[requisito], idTarefa(tarefa))
		if api.StatusTarefa(status) == api.Cancelada {
			// Não queremos mais fazê-la: fica na lista, para o histórico, mas não é trabalho em aberto
			// nem entrega; para o estado e a atenção do requisito, é como se não existisse.
			continue
		}
		l.trabalho[requisito] = append(l.trabalho[requisito], tarefaLigada{api.StatusTarefa(status), lerMomento(atualizada)})
		if api.StatusTarefa(status) != api.Concluida {
			l.abertas[requisito] = append(l.abertas[requisito], idTarefa(tarefa))
		}
	}
	return l, linhas.Err()
}

// pontosEfetivos: os da ligação, ou 1 se ela ainda não foi triada.
func (l ligacoes) pontosEfetivos(filho, pai int64) int {
	if p, ok := l.pontos[ligacao{filho, pai}]; ok {
		return p
	}
	return 1
}

// fatia é a parte do filho entre os irmãos sob o pai: pontos / soma dos pontos dos irmãos.
// O obsoleto não tem fatia nem entra na soma.
func (l ligacoes) fatia(filho, pai int64) float64 {
	if l.obsoletos[filho] {
		return 0
	}
	soma := 0
	for _, irmao := range l.vivos(pai) {
		soma += l.pontosEfetivos(irmao, pai)
	}
	if soma == 0 {
		return 0
	}
	return float64(l.pontosEfetivos(filho, pai)) / float64(soma)
}

// peso no total: o zero (sem pais) vale 1; os demais somam, sobre os pais, o peso do pai vezes a
// fatia sob ele. A soma das folhas fecha 1.
func (l ligacoes) peso(n int64) float64 {
	if p, ok := l.pesos[n]; ok {
		return p
	}
	l.pesos[n] = 0 // a derivação não tem ciclo; isto só protege de dados inconsistentes
	pais := l.paisN(n)
	p := 0.0
	if len(pais) == 0 {
		p = 1
	}
	for _, pai := range pais {
		p += l.peso(pai) * l.fatia(n, pai)
	}
	l.pesos[n] = p
	return p
}

// vivos: os derivados de n que não estão obsoletos; só eles contam no estado e no peso.
func (l ligacoes) vivos(n int64) []int64 {
	var vivos []int64
	for _, f := range l.filhosN[n] {
		if !l.obsoletos[f] {
			vivos = append(vivos, f)
		}
	}
	return vivos
}

// paisN devolve os pais de n por número.
func (l ligacoes) paisN(n int64) []int64 {
	var pais []int64
	for _, p := range l.pais[n] {
		pais = append(pais, mustNumero(p, "R"))
	}
	return pais
}

func carregarOrganizacoesDosRequisitos(c consultor, l *ligacoes) error {
	linhas, err := c.Query(`SELECT id, organizacao, obsoleto_em IS NOT NULL FROM requisitos`)
	if err != nil {
		return err
	}
	for linhas.Next() {
		var n int64
		var org string
		var obsoleto bool
		if err := linhas.Scan(&n, &org, &obsoleto); err != nil {
			linhas.Close()
			return err
		}
		l.orgs[n] = org
		if obsoleto {
			l.obsoletos[n] = true
		}
	}
	if err := linhas.Close(); err != nil {
		return err
	}
	linhas, err = c.Query(`SELECT zero_id FROM organizacoes WHERE zero_id IS NOT NULL`)
	if err != nil {
		return err
	}
	defer linhas.Close()
	for linhas.Next() {
		var n int64
		if err := linhas.Scan(&n); err != nil {
			return err
		}
		l.zeros[n] = true
	}
	return linhas.Err()
}

func carregarComponentesDosRequisitos(c consultor, l *ligacoes) error {
	linhas, err := c.Query(`SELECT requisito_id, componente_id FROM requisito_componente ORDER BY componente_id`)
	if err != nil {
		return err
	}
	defer linhas.Close()
	for linhas.Next() {
		var r, k int64
		if err := linhas.Scan(&r, &k); err != nil {
			return err
		}
		l.componentes[r] = append(l.componentes[r], idComponente(k))
	}
	return linhas.Err()
}

func naoNulo(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

const colunasRequisito = `id, titulo, descricao, organizacao, origem_id, criado_em, atualizado_em, prioridade, obsoleto_em, motivo_obsoleto`

func lerRequisito(linha varredor, l ligacoes) (api.Requisito, error) {
	var x api.Requisito
	var n int64
	var origem sql.NullInt64
	var criado, atualizado string
	var prioridade, obsoletoEm, motivo sql.NullString
	if err := linha.Scan(&n, &x.Titulo, &x.Descricao, &x.Organizacao, &origem, &criado, &atualizado, &prioridade, &obsoletoEm, &motivo); err != nil {
		return x, err
	}
	if obsoletoEm.Valid {
		x.Obsoleto = &api.Obsoleto{Em: lerMomento(obsoletoEm.String), Motivo: motivo.String}
	}
	x.Id, x.CriadoEm, x.AtualizadoEm = idRequisito(n), lerMomento(criado), lerMomento(atualizado)
	if prioridade.Valid {
		p := api.Prioridade(prioridade.String)
		x.Prioridade = &p
	}
	if origem.Valid {
		o := idOrigem(origem.Int64)
		x.Origem = &o
	}
	x.DerivaDe, x.Derivados, x.Tarefas = naoNulo(l.pais[n]), naoNulo(l.filhos[n]), naoNulo(l.tarefas[n])
	x.Importancia = []api.Importancia{}
	for _, pai := range l.paisN(n) {
		i := api.Importancia{Pai: idRequisito(pai), Fatia: float32(l.fatia(n, pai))}
		if p, ok := l.pontos[ligacao{n, pai}]; ok {
			i.Pontos = &p
		}
		x.Importancia = append(x.Importancia, i)
	}
	x.Peso = float32(l.peso(n))
	x.Componentes = naoNulo(l.componentes[n])
	x.Atencao = atencao(x, len(l.vivos(n)), l.trabalho[n], l.agora)
	if x.Obsoleto != nil {
		// Do obsoleto, só importa o que ainda está pendurado nele: as tarefas abertas.
		x.Atencao = []api.Atencao{}
		if abertas := l.abertas[n]; len(abertas) > 0 {
			x.Atencao = append(x.Atencao, api.Atencao{Motivo: api.ObsoletoComTarefaAberta, Detalhe: fmt.Sprintf(
				"tarefas abertas (%s): conclua, ligue a outro requisito ou desvincule cada uma", strings.Join(abertas, ", "))})
		}
	} else if !l.zeros[n] {
		for _, pai := range l.paisN(n) {
			if l.orgs[pai] != x.Organizacao {
				x.Atencao = append(x.Atencao, api.Atencao{Motivo: api.OutraOrganizacao, Detalhe: fmt.Sprintf(
					"deriva de %s, da organização %s: na visão de %s ele fica fora da cadeia (só o zero da organização deriva de fora dela)",
					idRequisito(pai), l.orgs[pai], x.Organizacao)})
			}
		}
	}
	x.TarefasAbertas = naoNulo(l.abertas[n])
	if e, ok := l.ultimas[n]; ok {
		x.UltimaExecucao = &e
	}
	x.EstadoProprio = l.estadoProprio(n)
	x.Estado = l.estado(n)
	x.LimitadoPor = []string{}
	if l.soPelosDerivados(n) {
		x.EstadoProprio = x.Estado
	}
	if maturidade[x.Estado] < maturidade[l.estadoProprio(n)] {
		for _, f := range l.vivos(n) {
			if l.estado(f) == x.Estado {
				x.LimitadoPor = append(x.LimitadoPor, idRequisito(f))
			}
		}
	}
	return x, nil
}

// maturidade ordena os estados, do menos ao mais maduro.
var maturidade = map[api.EstadoDoRequisito]int{
	api.NaoAtendido: 0, api.EmTrabalho: 1, api.NaoValidado: 2, api.ValidacaoExpirada: 3, api.Validado: 4,
}

func menosMaduro(a, b api.EstadoDoRequisito) api.EstadoDoRequisito {
	if maturidade[b] < maturidade[a] {
		return b
	}
	return a
}

// soPelosDerivados: sem tarefa e sem execução, um requisito que se desdobra em outros é verificado
// por eles; o estado dele é o dos derivados.
func (l ligacoes) soPelosDerivados(n int64) bool {
	_, executado := l.ultimas[n]
	return len(l.trabalho[n]) == 0 && !executado && len(l.vivos(n)) > 0
}

// estadoProprio vem só das tarefas ligadas e da última execução. A tarefa menos madura manda:
// aberta fora de andamento ⇒ não atendido; em andamento ⇒ em trabalho. Tarefas concluídas não provam
// nada (as canceladas nem entram em trabalho): sem tarefa aberta, quem decide é a última execução. Sem tarefa e sem execução, não há nada
// que indique atendimento: não atendido (ou, com derivados, neutro: decidem eles).
func (l ligacoes) estadoProprio(n int64) api.EstadoDoRequisito {
	e := api.Validado
	aberta := false
	for _, t := range l.trabalho[n] {
		switch t.status {
		case api.Concluida:
		case api.EmAndamento:
			aberta, e = true, menosMaduro(e, api.EmTrabalho)
		default:
			aberta, e = true, api.NaoAtendido
		}
	}
	if aberta {
		return e
	}
	ultima, ok := l.ultimas[n]
	switch {
	case !ok && l.soPelosDerivados(n):
		return api.Validado
	case !ok && len(l.trabalho[n]) == 0:
		return api.NaoAtendido
	case !ok:
		return api.NaoValidado
	case ultima.ValidaAte != nil && !ultima.ValidaAte.After(l.agora):
		return api.ValidacaoExpirada
	}
	return api.Validado
}

// estado é limitado pelo menos maduro abaixo dele: o próprio e o de cada derivado vivo, recursivamente.
func (l ligacoes) estado(n int64) api.EstadoDoRequisito {
	if e, ok := l.estados[n]; ok {
		return e
	}
	e := l.estadoProprio(n)
	l.estados[n] = e // a derivação não tem ciclo; isto só protege de dados inconsistentes
	for _, f := range l.vivos(n) {
		e = menosMaduro(e, l.estado(f))
	}
	l.estados[n] = e
	return e
}

// atencao lista o que falta para o requisito estar bem cuidado.
// vivos é quantos derivados não estão obsoletos.
func atencao(x api.Requisito, vivos int, trabalho []tarefaLigada, agora time.Time) []api.Atencao {
	motivos := []api.Atencao{}
	add := func(m api.MotivoDeAtencao, detalhe string, args ...any) {
		motivos = append(motivos, api.Atencao{Motivo: m, Detalhe: fmt.Sprintf(detalhe, args...)})
	}
	if vivos > MaxFilhos {
		add(api.MuitosFilhos, "%d requisitos derivam dele; a boa prática é até %d: agrupe-os sob requisitos intermediários", vivos, MaxFilhos)
	}
	if x.Origem == nil && len(x.DerivaDe) > 0 {
		add(api.SemOrigem, "não diz de onde vem (pessoa ou documento)")
	}
	if vazio(x.Descricao) {
		add(api.SemDescricao, "sem descrição: por que existe e como verificar")
	}
	if vivos == 0 && len(trabalho) == 0 {
		add(api.SemTarefa, "nenhum requisito abaixo e nenhuma tarefa tentando atendê-lo")
	}
	if len(trabalho) > 0 {
		parado, ultima := true, time.Time{}
		for _, t := range trabalho {
			if t.status != api.Pausada && t.status != api.Bloqueada {
				parado = false
			}
			if t.atualizada.After(ultima) {
				ultima = t.atualizada
			}
		}
		if dias := int(agora.Sub(ultima).Hours() / 24); parado && dias >= diasParado {
			add(api.TrabalhoParado, "todas as tarefas estão pausadas ou bloqueadas há %d dias", dias)
		}
	}
	return motivos
}

func carregarRequisito(c consultor, n int64) (api.Requisito, error) {
	l, err := carregarLigacoes(c)
	if err != nil {
		return api.Requisito{}, err
	}
	x, err := lerRequisito(c.QueryRow(`SELECT `+colunasRequisito+` FROM requisitos WHERE id = ?`, n), l)
	if errors.Is(err, sql.ErrNoRows) {
		return x, falha(http.StatusNotFound, "requisito %s não encontrado", idRequisito(n))
	}
	return x, err
}

func existeRequisito(c consultor, n int64) error {
	var um int
	err := c.QueryRow(`SELECT 1 FROM requisitos WHERE id = ?`, n).Scan(&um)
	if errors.Is(err, sql.ErrNoRows) {
		return falha(http.StatusNotFound, "requisito %s não encontrado", idRequisito(n))
	}
	return err
}

// raiz devolve o requisito zero, o único que não deriva de nenhum, ou 0 se ainda não há requisitos.
func raiz(c consultor) (int64, error) {
	var n int64
	err := c.QueryRow(`SELECT id FROM requisitos r WHERE NOT EXISTS (SELECT 1 FROM derivacoes d WHERE d.requisito_id = r.id)
		ORDER BY id LIMIT 1`).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

// derivar liga filho → pai, recusando o que fecharia um ciclo: o pai não pode já descender do filho.
func derivar(tx *sql.Tx, filho, pai int64, pontos *int) error {
	if filho == pai {
		return falha(http.StatusBadRequest, "um requisito não deriva de si mesmo")
	}
	if err := existeRequisito(tx, pai); err != nil {
		return err
	}
	if err := recusarObsoleto(tx, pai, "derivar dele"); err != nil {
		return err
	}
	var ciclo int
	err := tx.QueryRow(`WITH RECURSIVE abaixo(id) AS (
			SELECT ? UNION SELECT d.requisito_id FROM derivacoes d JOIN abaixo a ON d.pai_id = a.id)
		SELECT count(*) FROM abaixo WHERE id = ?`, filho, pai).Scan(&ciclo)
	if err != nil {
		return err
	}
	if ciclo > 0 {
		return falha(http.StatusConflict, "%s já deriva de %s; a derivação fecharia um ciclo", idRequisito(pai), idRequisito(filho))
	}
	if pontos != nil && *pontos < 1 {
		return falha(http.StatusBadRequest, "pontos inválidos: %d (mínimo 1)", *pontos)
	}
	_, err = tx.Exec(`INSERT OR IGNORE INTO derivacoes (requisito_id, pai_id, pontos) VALUES (?, ?, ?)`, filho, pai, pontos)
	return err
}

func (s *Servidor) ListarRequisitos(w http.ResponseWriter, r *http.Request, params api.ListarRequisitosParams) {
	consulta := `SELECT ` + colunasRequisito + ` FROM requisitos WHERE 1 = 1`
	var args []any
	if params.Organizacao != nil {
		consulta += ` AND organizacao = ?`
		args = append(args, *params.Organizacao)
	}
	if params.Origem != nil {
		n, err := numero(*params.Origem, "O")
		if err != nil {
			responderErro(w, err)
			return
		}
		consulta += ` AND origem_id = ?`
		args = append(args, n)
	}
	l, err := carregarLigacoes(s.db)
	if err != nil {
		responderErro(w, err)
		return
	}
	linhas, err := s.db.Query(consulta+` ORDER BY id`, args...)
	if err != nil {
		responderErro(w, err)
		return
	}
	defer linhas.Close()
	requisitos := []api.Requisito{}
	for linhas.Next() {
		x, err := lerRequisito(linhas, l)
		if err != nil {
			responderErro(w, err)
			return
		}
		requisitos = append(requisitos, x)
	}
	if err := linhas.Err(); err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusOK, requisitos)
}

// origemOpcional converte "O-0001" (ou vazio, que remove) no valor da coluna.
func origemOpcional(c consultor, id string) (any, error) {
	if vazio(id) {
		return nil, nil
	}
	n, err := numero(id, "O")
	if err != nil {
		return nil, err
	}
	if _, err := carregarOrigem(c, n); err != nil {
		return nil, err
	}
	return n, nil
}

func (s *Servidor) CriarRequisito(w http.ResponseWriter, r *http.Request) {
	var novo api.NovoRequisito
	if err := ler(r, &novo); err != nil {
		responderErro(w, err)
		return
	}
	var criado api.Requisito
	err := s.emTransacao(func(tx *sql.Tx) error {
		quem, err := autor(tx, r)
		if err != nil {
			return err
		}
		criado, err = s.inserirRequisito(tx, quem, novo)
		return err
	})
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusCreated, criado)
}

// inserirRequisito valida e grava um requisito novo, com as derivações e o evento.
func (s *Servidor) inserirRequisito(tx *sql.Tx, quem string, novo api.NovoRequisito) (criado api.Requisito, err error) {
	if vazio(novo.Titulo) {
		return criado, falha(http.StatusBadRequest, "o título é obrigatório")
	}
	var pais []int64
	if novo.DerivaDe != nil {
		for _, id := range *novo.DerivaDe {
			n, err := numero(id, "R")
			if err != nil {
				return criado, err
			}
			if err := existeRequisito(tx, n); err != nil {
				return criado, err
			}
			pais = append(pais, n)
		}
	}
	// Um só zero: o primeiro requisito é ele; os demais derivam de algo (padrão: do zero).
	if len(pais) == 0 {
		zero, err := raiz(tx)
		if err != nil {
			return criado, err
		}
		if zero != 0 {
			pais = append(pais, zero)
		}
	}
	organizacao := ""
	if novo.Organizacao != nil {
		organizacao = strings.TrimSpace(*novo.Organizacao)
	}
	if organizacao == "" && len(pais) > 0 {
		// Padrão herdado do pai.
		if err := tx.QueryRow(`SELECT organizacao FROM requisitos WHERE id = ?`, pais[0]).Scan(&organizacao); err != nil {
			return criado, err
		}
	}
	if organizacao == "" {
		return criado, falha(http.StatusBadRequest, "a organização é obrigatória (ou derive de um requisito para herdá-la)")
	}
	var origem any
	if novo.Origem != nil {
		if origem, err = origemOpcional(tx, *novo.Origem); err != nil {
			return criado, err
		}
	}
	descricao := ""
	if novo.Descricao != nil {
		descricao = *novo.Descricao
	}
	agora := s.momento()
	res, err := tx.Exec(`INSERT INTO requisitos (titulo, descricao, organizacao, origem_id, criado_em, atualizado_em)
		VALUES (?, ?, ?, ?, ?, ?)`, strings.TrimSpace(novo.Titulo), descricao, organizacao, origem, agora, agora)
	if err != nil {
		return criado, err
	}
	n, err := res.LastInsertId()
	if err != nil {
		return criado, err
	}
	for _, pai := range pais {
		if err := derivar(tx, n, pai, nil); err != nil {
			return criado, err
		}
	}
	if criado, err = carregarRequisito(tx, n); err != nil {
		return criado, err
	}
	return criado, s.registrarEvento(tx, quem, criado.Id, "requisito_criado", map[string]any{
		"titulo": criado.Titulo, "organizacao": criado.Organizacao, "origem": criado.Origem, "deriva_de": criado.DerivaDe,
	})
}

func (s *Servidor) ObterRequisito(w http.ResponseWriter, r *http.Request, id api.Id) {
	n, err := numero(id, "R")
	if err != nil {
		responderErro(w, err)
		return
	}
	x, err := carregarRequisito(s.db, n)
	if err != nil {
		responderErro(w, err)
		return
	}
	todos, _, err := s.todosOsRequisitos()
	if err != nil {
		responderErro(w, err)
		return
	}
	porId := map[string]api.Requisito{}
	for _, r := range todos {
		porId[r.Id] = r
	}
	t := trilhas(x, porId)
	x.Trilhas = &t
	responder(w, http.StatusOK, x)
}

func (s *Servidor) AlterarRequisito(w http.ResponseWriter, r *http.Request, id api.Id) {
	n, err := numero(id, "R")
	if err != nil {
		responderErro(w, err)
		return
	}
	var alteracao api.AlteracaoRequisito
	if err := ler(r, &alteracao); err != nil {
		responderErro(w, err)
		return
	}
	var alterado api.Requisito
	err = s.emTransacao(func(tx *sql.Tx) error {
		quem, err := autor(tx, r)
		if err != nil {
			return err
		}
		if err := existeRequisito(tx, n); err != nil {
			return err
		}
		campos := map[string]any{}
		var sets []string
		var args []any
		definir := func(coluna string, valor any, registro any) {
			sets = append(sets, coluna+" = ?")
			args = append(args, valor)
			campos[coluna] = registro
		}
		if alteracao.Titulo != nil {
			if vazio(*alteracao.Titulo) {
				return falha(http.StatusBadRequest, "o título não pode ficar vazio")
			}
			definir("titulo", strings.TrimSpace(*alteracao.Titulo), *alteracao.Titulo)
		}
		if alteracao.Descricao != nil {
			definir("descricao", *alteracao.Descricao, *alteracao.Descricao)
		}
		if alteracao.Organizacao != nil {
			if vazio(*alteracao.Organizacao) {
				return falha(http.StatusBadRequest, "a organização não pode ficar vazia")
			}
			definir("organizacao", strings.TrimSpace(*alteracao.Organizacao), *alteracao.Organizacao)
		}
		if alteracao.Origem != nil {
			origem, err := origemOpcional(tx, *alteracao.Origem)
			if err != nil {
				return err
			}
			definir("origem_id", origem, *alteracao.Origem)
		}
		if alteracao.Prioridade != nil {
			var valor any
			switch p := api.Prioridade(*alteracao.Prioridade); p {
			case "":
			case api.Alta, api.Media, api.Baixa:
				valor = string(p)
			default:
				return falha(http.StatusBadRequest, "prioridade inválida: %q (alta, media, baixa ou vazio)", p)
			}
			definir("prioridade", valor, *alteracao.Prioridade)
		}
		if len(sets) == 0 {
			return falha(http.StatusBadRequest, "nenhum campo para alterar")
		}
		sets = append(sets, "atualizado_em = ?")
		args = append(args, s.momento(), n)
		if _, err := tx.Exec(`UPDATE requisitos SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
			return err
		}
		if alterado, err = carregarRequisito(tx, n); err != nil {
			return err
		}
		return s.registrarEvento(tx, quem, alterado.Id, "requisito_alterado", campos)
	})
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusOK, alterado)
}

// mudarCadeia roda uma alteração de derivação no requisito e devolve o requisito atualizado.
func (s *Servidor) mudarCadeia(w http.ResponseWriter, r *http.Request, id string, corpo any,
	f func(tx *sql.Tx, n int64, quem string) error) {
	n, err := numero(id, "R")
	if err != nil {
		responderErro(w, err)
		return
	}
	if corpo != nil {
		if err := ler(r, corpo); err != nil {
			responderErro(w, err)
			return
		}
	}
	var x api.Requisito
	err = s.emTransacao(func(tx *sql.Tx) error {
		quem, err := autor(tx, r)
		if err != nil {
			return err
		}
		if err := existeRequisito(tx, n); err != nil {
			return err
		}
		if err := f(tx, n, quem); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE requisitos SET atualizado_em = ? WHERE id = ?`, s.momento(), n); err != nil {
			return err
		}
		x, err = carregarRequisito(tx, n)
		return err
	})
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusOK, x)
}

func (s *Servidor) DerivarRequisito(w http.ResponseWriter, r *http.Request, id api.Id) {
	var d api.Derivacao
	s.mudarCadeia(w, r, id, &d, func(tx *sql.Tx, n int64, quem string) error {
		pai, err := numero(d.Pai, "R")
		if err != nil {
			return err
		}
		// Ao derivar de mais um pai, o padrão é 1 ponto: já triado, o menor peso entre os irmãos.
		pontos := d.Pontos
		if pontos == nil {
			var pais int
			if err := tx.QueryRow(`SELECT count(*) FROM derivacoes WHERE requisito_id = ? AND pai_id <> ?`, n, pai).Scan(&pais); err != nil {
				return err
			}
			if pais > 0 {
				um := 1
				pontos = &um
			}
		}
		if err := derivar(tx, n, pai, pontos); err != nil {
			return err
		}
		return s.registrarEvento(tx, quem, idRequisito(n), "derivacao_registrada", map[string]any{"pai": idRequisito(pai), "pontos": pontos})
	})
}

// DefinirPontos dá ao requisito os pontos de importância sob um pai; só as fatias dos irmãos mudam.
func (s *Servidor) DefinirPontos(w http.ResponseWriter, r *http.Request, id api.Id, paiID string) {
	var p api.Pontos
	s.mudarCadeia(w, r, id, &p, func(tx *sql.Tx, n int64, quem string) error {
		pai, err := numero(paiID, "R")
		if err != nil {
			return err
		}
		if p.Pontos != nil && *p.Pontos < 1 {
			return falha(http.StatusBadRequest, "pontos inválidos: %d (mínimo 1; sem pontos, volta a não triado)", *p.Pontos)
		}
		res, err := tx.Exec(`UPDATE derivacoes SET pontos = ? WHERE requisito_id = ? AND pai_id = ?`, p.Pontos, n, pai)
		if err != nil {
			return err
		}
		if afetadas, _ := res.RowsAffected(); afetadas == 0 {
			return falha(http.StatusNotFound, "%s não deriva de %s", idRequisito(n), idRequisito(pai))
		}
		return s.registrarEvento(tx, quem, idRequisito(n), "pontos_definidos", map[string]any{"pai": idRequisito(pai), "pontos": p.Pontos})
	})
}

func (s *Servidor) DesfazerDerivacao(w http.ResponseWriter, r *http.Request, id api.Id, paiID string) {
	s.mudarCadeia(w, r, id, nil, func(tx *sql.Tx, n int64, quem string) error {
		pai, err := numero(paiID, "R")
		if err != nil {
			return err
		}
		res, err := tx.Exec(`DELETE FROM derivacoes WHERE requisito_id = ? AND pai_id = ?`, n, pai)
		if err != nil {
			return err
		}
		if afetadas, _ := res.RowsAffected(); afetadas == 0 {
			return falha(http.StatusNotFound, "%s não deriva de %s", idRequisito(n), idRequisito(pai))
		}
		var restantes int
		if err := tx.QueryRow(`SELECT count(*) FROM derivacoes WHERE requisito_id = ?`, n).Scan(&restantes); err != nil {
			return err
		}
		if restantes == 0 {
			return falha(http.StatusConflict, "%s ficaria sem pai: todo requisito, menos o zero, deriva de algum; "+
				"acrescente o novo pai antes de desfazer este", idRequisito(n))
		}
		return s.registrarEvento(tx, quem, idRequisito(n), "derivacao_desfeita", map[string]any{"pai": idRequisito(pai)})
	})
}

// recusarObsoleto barra ligações novas a um requisito obsoleto: ele não é mais o porquê de nada.
func recusarObsoleto(c consultor, n int64, acao string) error {
	var obsoleto bool
	if err := c.QueryRow(`SELECT obsoleto_em IS NOT NULL FROM requisitos WHERE id = ?`, n).Scan(&obsoleto); err != nil {
		return err
	}
	if obsoleto {
		return falha(http.StatusConflict, "%s está obsoleto: não dá para %s (reative-o antes, se foi engano)", idRequisito(n), acao)
	}
	return nil
}

// MarcarObsoleto: o requisito fica no histórico, com quando e por quê, mas sai do cálculo dos pais.
func (s *Servidor) MarcarObsoleto(w http.ResponseWriter, r *http.Request, id api.Id) {
	var o api.Obsolescencia
	s.mudarCadeia(w, r, id, &o, func(tx *sql.Tx, n int64, quem string) error {
		if vazio(o.Motivo) {
			return falha(http.StatusBadRequest, "o motivo é obrigatório")
		}
		var pais int
		if err := tx.QueryRow(`SELECT count(*) FROM derivacoes WHERE requisito_id = ?`, n).Scan(&pais); err != nil {
			return err
		}
		if pais == 0 {
			return falha(http.StatusConflict, "%s é um zero: todos os outros derivam dele", idRequisito(n))
		}
		if err := recusarObsoleto(tx, n, "marcá-lo de novo"); err != nil {
			return err
		}
		motivo := strings.TrimSpace(o.Motivo)
		if _, err := tx.Exec(`UPDATE requisitos SET obsoleto_em = ?, motivo_obsoleto = ? WHERE id = ?`, s.momento(), motivo, n); err != nil {
			return err
		}
		return s.registrarEvento(tx, quem, idRequisito(n), "requisito_obsoleto", map[string]any{"motivo": motivo})
	})
}

// ReativarRequisito desfaz a obsolescência (marcada por engano); o evento guarda o motivo que havia.
func (s *Servidor) ReativarRequisito(w http.ResponseWriter, r *http.Request, id api.Id) {
	s.mudarCadeia(w, r, id, nil, func(tx *sql.Tx, n int64, quem string) error {
		var motivo sql.NullString
		if err := tx.QueryRow(`SELECT motivo_obsoleto FROM requisitos WHERE id = ?`, n).Scan(&motivo); err != nil {
			return err
		}
		if !motivo.Valid {
			return falha(http.StatusConflict, "%s não está obsoleto", idRequisito(n))
		}
		if _, err := tx.Exec(`UPDATE requisitos SET obsoleto_em = NULL, motivo_obsoleto = NULL WHERE id = ?`, n); err != nil {
			return err
		}
		return s.registrarEvento(tx, quem, idRequisito(n), "requisito_reativado", map[string]any{"motivo_obsoleto": motivo.String})
	})
}

// ---- Tarefa ↔ Requisito ----

func requisitosDaTarefa(c consultor, tarefa int64) ([]string, error) {
	linhas, err := c.Query(`SELECT requisito_id FROM tarefa_requisito WHERE tarefa_id = ? ORDER BY requisito_id`, tarefa)
	if err != nil {
		return nil, err
	}
	defer linhas.Close()
	ids := []string{}
	for linhas.Next() {
		var n int64
		if err := linhas.Scan(&n); err != nil {
			return nil, err
		}
		ids = append(ids, idRequisito(n))
	}
	return ids, linhas.Err()
}

func (s *Servidor) mudarVinculoDeRequisito(w http.ResponseWriter, r *http.Request, id, requisito string, vincular bool) {
	t, err := numero(id, "T")
	if err != nil {
		responderErro(w, err)
		return
	}
	var tarefa api.Tarefa
	err = s.emTransacao(func(tx *sql.Tx) error {
		quem, err := autor(tx, r)
		if err != nil {
			return err
		}
		if _, err := carregarTarefa(tx, t); err != nil {
			return err
		}
		n, err := numero(requisito, "R")
		if err != nil {
			return err
		}
		if err := existeRequisito(tx, n); err != nil {
			return err
		}
		if vincular {
			if err := recusarObsoleto(tx, n, "vincular tarefas a ele"); err != nil {
				return err
			}
		}
		tipo := "requisito_vinculado"
		if vincular {
			_, err = tx.Exec(`INSERT OR IGNORE INTO tarefa_requisito (tarefa_id, requisito_id) VALUES (?, ?)`, t, n)
		} else {
			tipo = "requisito_desvinculado"
			_, err = tx.Exec(`DELETE FROM tarefa_requisito WHERE tarefa_id = ? AND requisito_id = ?`, t, n)
		}
		if err != nil {
			return err
		}
		if err := s.registrarEvento(tx, quem, idTarefa(t), tipo, map[string]any{"requisito": idRequisito(n)}); err != nil {
			return err
		}
		tarefa, err = carregarTarefa(tx, t)
		return err
	})
	if err != nil {
		responderErro(w, err)
		return
	}
	responder(w, http.StatusOK, tarefa)
}

func (s *Servidor) VincularRequisito(w http.ResponseWriter, r *http.Request, id api.Id) {
	var v api.VinculoRequisito
	if err := ler(r, &v); err != nil {
		responderErro(w, err)
		return
	}
	s.mudarVinculoDeRequisito(w, r, id, v.Requisito, true)
}

func (s *Servidor) DesvincularRequisito(w http.ResponseWriter, r *http.Request, id api.Id, requisito string) {
	s.mudarVinculoDeRequisito(w, r, id, requisito, false)
}
