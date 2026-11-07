import { useState } from "react"
import { Link, useParams } from "react-router-dom"

import {
  type AlteracaoLembrete,
  type Lembrete,
  type PrecisaoLembrete,
  useAdiarLembrete,
  useAlterarLembrete,
  useCancelarLembrete,
  useConfirmarLembrete,
  useCriarLembrete,
  useLembretes,
  useOrganizacoes,
} from "@/api/cliente"
import { Estado } from "@/componentes/Estado"
import { StatusDoLembrete } from "@/componentes/rotulos"
import { Secao } from "@/componentes/Secao"
import { useOrganizacaoAtual } from "@/hooks/useOrganizacaoAtual"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { cn } from "@/lib/utils"

const formatoDaHora = new Intl.DateTimeFormat("pt-BR", { weekday: "short", day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" })
const formatoDoDia = new Intl.DateTimeFormat("pt-BR", { weekday: "short", day: "2-digit", month: "2-digit" })
const formatoDaSemana = new Intl.DateTimeFormat("pt-BR", { day: "2-digit", month: "2-digit" })
const formatoDoMes = new Intl.DateTimeFormat("pt-BR", { month: "long", year: "numeric" })
const formatoDoFim = new Intl.DateTimeFormat("pt-BR", { weekday: "short", day: "2-digit", month: "2-digit", hour: "2-digit" })

type Turno = "manha" | "almoco" | "tarde" | "noite"
const nomesDoTurno: Record<Turno, string> = { manha: "manhã", almoco: "almoço", tarde: "tarde", noite: "noite" }
// Uma hora dentro de cada turno, para o servidor pôr no início dele (os turnos são da instalação;
// antes da manhã conta como manhã, e 23:59 é sempre noite).
const horaDentroDoTurno: Record<Turno, string> = { manha: "00:00", almoco: "12:30", tarde: "15:00", noite: "23:59" }

// O turno de um lembrete pela hora de início (aproximado: os limites exatos são da instalação).
function turnoDe(quando: string): Turno {
  const h = new Date(quando).getHours()
  return h < 12 ? "manha" : h < 13 ? "almoco" : h < 18 ? "tarde" : "noite"
}

// Até quando vale a janela de semana, fim de semana ou mês ("até sex., 13/11, 18h"); o fim é exclusivo,
// então meia-noite vira o dia anterior.
function ate(l: Lembrete): string {
  if (!l.fim) return ""
  const fim = new Date(l.fim)
  if (fim.getHours() === 0 && fim.getMinutes() === 0) return ` · até ${formatoDoDia.format(new Date(fim.getTime() - 1))}`
  return ` · até ${formatoDoFim.format(fim)}h`
}

function descreverQuando(l: Lembrete): string {
  const d = new Date(l.quando)
  if (l.precisao === "mes") return `${formatoDoMes.format(d)}${ate(l)}`
  if (l.precisao === "semana") return `semana de ${formatoDaSemana.format(d)}${ate(l)}`
  if (l.precisao === "fim_de_semana") return `fim de semana de ${formatoDaSemana.format(d)}${ate(l)}`
  if (l.precisao === "dia") return `${formatoDoDia.format(d)} · o dia todo`
  if (l.precisao === "turno") return `${formatoDoDia.format(d)} · ${nomesDoTurno[turnoDe(l.quando)]}`
  return formatoDaHora.format(d)
}

// A recorrência por extenso: "todo mês", "a cada 2 semanas".
function descreverRecorrencia(l: Lembrete): string | undefined {
  if (!l.recorrencia) return undefined
  const n = l.intervalo ?? 1
  const unidade = { diaria: ["todo dia", "dias"], semanal: ["toda semana", "semanas"], mensal: ["todo mês", "meses"], anual: ["todo ano", "anos"] }[l.recorrencia]
  return n === 1 ? unidade[0] : `a cada ${n} ${unidade[1]}`
}

// Lembretes: o que está tocando primeiro, depois os próximos; quem avisa no celular
// e na barra são as integrações da instalação.
export function Lembretes() {
  const { id } = useParams()
  const pendentes = useLembretes()
  const encerrados = useLembretes(["confirmado", "cancelado"])
  const lista = pendentes.data ?? []
  const tocando = lista.filter((l) => l.status === "disparado")
  const proximos = lista.filter((l) => l.status !== "disparado")

  return (
    <div className="space-y-6">
      <h1 className="font-semibold text-xl">Lembretes</h1>
      <NovoLembrete />
      <Estado carregando={pendentes.isLoading} erro={pendentes.error} />
      {tocando.length > 0 && (
        <Secao titulo="Tocando">
          <ListaDeLembretes lembretes={tocando} destaque={id} />
        </Secao>
      )}
      <Secao titulo="Próximos">
        {proximos.length === 0 ? (
          <p className="text-muted-foreground text-sm">Nenhum lembrete agendado.</p>
        ) : (
          <ListaDeLembretes lembretes={proximos} destaque={id} />
        )}
      </Secao>
      <Secao titulo="Encerrados" recolhida>
        <ListaDeLembretes lembretes={[...(encerrados.data ?? [])].reverse()} destaque={id} />
      </Secao>
    </div>
  )
}

function ListaDeLembretes({ lembretes, destaque }: { lembretes: Lembrete[]; destaque?: string }) {
  return (
    <ul className="divide-y rounded-md border">
      {lembretes.map((l) => (
        <ItemDeLembrete key={l.id} lembrete={l} destacado={l.id === destaque} />
      ))}
    </ul>
  )
}

function ItemDeLembrete({ lembrete: l, destacado }: { lembrete: Lembrete; destacado: boolean }) {
  const confirmar = useConfirmarLembrete()
  const adiar = useAdiarLembrete()
  const cancelar = useCancelarLembrete()
  const [editando, setEditando] = useState(false)
  const aberto = l.status !== "confirmado" && l.status !== "cancelado"
  const erro = confirmar.error ?? adiar.error ?? cancelar.error
  return (
    <li className={cn("space-y-2 px-3 py-2.5", destacado && "bg-muted/60")}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span>
          <span className="font-mono text-muted-foreground text-xs">{l.id}</span> {l.texto}
        </span>
        <StatusDoLembrete status={l.status} />
      </div>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-muted-foreground text-xs">
        <span>{descreverQuando(l)}</span>
        {l.recorrencia && <span title={l.serie ? `série de ${l.serie}` : undefined}>↻ {descreverRecorrencia(l)}</span>}
        {l.seguinte && <span>próximo: {l.seguinte}</span>}
        {l.atrasado && l.status === "disparado" && <span>tocou atrasado</span>}
        {l.adiamentos > 0 && <span>adiado {l.adiamentos}×</span>}
        <span>{l.organizacao}</span>
        {l.tarefa && (
          <Link to={`/tarefas/${l.tarefa}`} className="hover:underline">
            {l.tarefa}
          </Link>
        )}
        {l.requisito && (
          <Link to={`/requisitos/${l.requisito}`} className="hover:underline">
            {l.requisito}
          </Link>
        )}
        <span>por {l.autor}</span>
      </div>
      {aberto && editando && <EditarLembrete lembrete={l} aoTerminar={() => setEditando(false)} />}
      {aberto && !editando && (
        <div className="flex flex-wrap gap-2">
          <Button size="sm" onClick={() => confirmar.mutate(l.id)} disabled={confirmar.isPending}>
            Confirmar
          </Button>
          <Button size="sm" variant="outline" onClick={() => adiar.mutate({ id: l.id, minutos: 10 })} disabled={adiar.isPending}>
            +10 min
          </Button>
          <Button size="sm" variant="outline" onClick={() => adiar.mutate({ id: l.id, minutos: 60 })} disabled={adiar.isPending}>
            +1 h
          </Button>
          <Button size="sm" variant="outline" onClick={() => setEditando(true)}>
            Editar
          </Button>
          <Button size="sm" variant="ghost" onClick={() => cancelar.mutate(l.id)} disabled={cancelar.isPending}>
            Cancelar
          </Button>
        </div>
      )}
      {erro && <p className="text-destructive text-sm">{erro.message}</p>}
    </li>
  )
}

// datetime-local não tem fuso: a hora vale no fuso do navegador e vai com o deslocamento dele.
function comFuso(local: string): string {
  const d = new Date(local)
  const minutos = -d.getTimezoneOffset()
  const sinal = minutos >= 0 ? "+" : "-"
  const dois = (n: number) => String(Math.floor(Math.abs(n))).padStart(2, "0")
  return `${local}:00${sinal}${dois(minutos / 60)}:${dois(minutos % 60)}`
}

// A hora do lembrete no fuso do navegador, no formato do datetime-local.
function horaLocal(quando: string): string {
  const d = new Date(quando)
  const dois = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${dois(d.getMonth() + 1)}-${dois(d.getDate())}T${dois(d.getHours())}:${dois(d.getMinutes())}`
}

// Marcacao é o que o formulário guarda de quando: data e hora (hora marcada) ou só a data (turno e
// dia), e o turno.
type Marcacao = { precisao: PrecisaoLembrete; local: string; turno: Turno }

const marcacaoVazia: Marcacao = { precisao: "hora", local: "", turno: "manha" }

function marcacaoDe(l: Lembrete): Marcacao {
  const local = horaLocal(l.quando)
  return { precisao: l.precisao, local: l.precisao === "hora" ? local : local.slice(0, 10), turno: turnoDe(l.quando) }
}

function quandoDe(m: Marcacao): string {
  if (m.precisao === "hora") return comFuso(m.local)
  return comFuso(`${m.local}T${m.precisao === "turno" ? horaDentroDoTurno[m.turno] : "12:00"}`)
}

// Trocar a precisão mantém a data já escolhida.
function trocarPrecisao(m: Marcacao, precisao: PrecisaoLembrete): Marcacao {
  const data = m.local.slice(0, 10)
  return { ...m, precisao, local: precisao === "hora" ? (data ? `${data}T09:00` : "") : data }
}

// Hora marcada (com alarme) ou um turno, dia, semana, fim de semana ou mês (avisados em lote, sem som).
// Em semana, fim de semana e mês, qualquer dia dentro dela serve: o servidor põe no início e grava o fim.
function CamposDeQuando({ prefixo, marcacao: m, mudar }: { prefixo: string; marcacao: Marcacao; mudar: (m: Marcacao) => void }) {
  return (
    <>
      <div className="space-y-1.5">
        <Label htmlFor={`${prefixo}-precisao`}>Precisão</Label>
        <Select value={m.precisao} onValueChange={(v) => mudar(trocarPrecisao(m, v as PrecisaoLembrete))}>
          <SelectTrigger id={`${prefixo}-precisao`} className="w-40">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="hora">Hora marcada</SelectItem>
            <SelectItem value="turno">Turno</SelectItem>
            <SelectItem value="dia">Dia</SelectItem>
            <SelectItem value="semana">Semana (até sexta)</SelectItem>
            <SelectItem value="fim_de_semana">Fim de semana</SelectItem>
            <SelectItem value="mes">Mês</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor={`${prefixo}-quando`}>Quando</Label>
        <div className="flex gap-2">
          <Input
            id={`${prefixo}-quando`}
            type={m.precisao === "hora" ? "datetime-local" : "date"}
            value={m.local}
            onChange={(e) => mudar({ ...m, local: e.target.value })}
            required
          />
          {m.precisao === "turno" && (
            <Select value={m.turno} onValueChange={(v) => mudar({ ...m, turno: v as Turno })}>
              <SelectTrigger aria-label="Turno" className="w-28">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="manha">Manhã</SelectItem>
                <SelectItem value="almoco">Almoço</SelectItem>
                <SelectItem value="tarde">Tarde</SelectItem>
                <SelectItem value="noite">Noite</SelectItem>
              </SelectContent>
            </Select>
          )}
        </div>
      </div>
    </>
  )
}

// Corrige o lembrete mantendo o id; vai só o que mudou (a hora, se mantida, não é reenviada: a de um
// lembrete disparado já passou e seria recusada; mudar a precisão reenvia a hora). Tarefa ou requisito vazio remove a ligação.
function EditarLembrete({ lembrete: l, aoTerminar }: { lembrete: Lembrete; aoTerminar: () => void }) {
  const alterar = useAlterarLembrete()
  const organizacoes = useOrganizacoes()
  const original = marcacaoDe(l)
  const [texto, setTexto] = useState(l.texto)
  const [marcacao, setMarcacao] = useState(original)
  const [organizacao, setOrganizacao] = useState(l.organizacao)
  const [tarefa, setTarefa] = useState(l.tarefa ?? "")
  const [requisito, setRequisito] = useState(l.requisito ?? "")
  const prefixo = `editar-${l.id}`
  return (
    <form
      className="flex flex-wrap items-end gap-3 rounded-md border p-3"
      onSubmit={(e) => {
        e.preventDefault()
        const alteracao: AlteracaoLembrete = {}
        if (texto.trim() !== l.texto) alteracao.texto = texto
        if (marcacao.precisao !== original.precisao) alteracao.precisao = marcacao.precisao
        if (alteracao.precisao || marcacao.local !== original.local || (marcacao.precisao === "turno" && marcacao.turno !== original.turno))
          alteracao.quando = quandoDe(marcacao)
        if (organizacao.trim() !== l.organizacao) alteracao.organizacao = organizacao
        if (tarefa.trim() !== (l.tarefa ?? "")) alteracao.tarefa = tarefa.trim()
        if (requisito.trim() !== (l.requisito ?? "")) alteracao.requisito = requisito.trim()
        if (Object.keys(alteracao).length === 0) {
          aoTerminar()
          return
        }
        alterar.mutate({ id: l.id, ...alteracao }, { onSuccess: aoTerminar })
      }}
    >
      <div className="min-w-48 flex-1 space-y-1.5">
        <Label htmlFor={`${prefixo}-texto`}>Lembrar de</Label>
        <Input id={`${prefixo}-texto`} value={texto} onChange={(e) => setTexto(e.target.value)} required />
      </div>
      <CamposDeQuando prefixo={prefixo} marcacao={marcacao} mudar={setMarcacao} />
      <div className="w-36 space-y-1.5">
        <Label htmlFor={`${prefixo}-organizacao`}>Organização</Label>
        <Input
          id={`${prefixo}-organizacao`}
          list={`${prefixo}-organizacoes`}
          value={organizacao}
          onChange={(e) => setOrganizacao(e.target.value)}
          required
        />
        <datalist id={`${prefixo}-organizacoes`}>
          {(organizacoes.data ?? []).map((o) => (
            <option key={o.nome} value={o.nome} />
          ))}
        </datalist>
      </div>
      <div className="w-28 space-y-1.5">
        <Label htmlFor={`${prefixo}-tarefa`}>Tarefa</Label>
        <Input id={`${prefixo}-tarefa`} placeholder="T-0001" value={tarefa} onChange={(e) => setTarefa(e.target.value)} />
      </div>
      <div className="w-28 space-y-1.5">
        <Label htmlFor={`${prefixo}-requisito`}>Requisito</Label>
        <Input id={`${prefixo}-requisito`} placeholder="R-0001" value={requisito} onChange={(e) => setRequisito(e.target.value)} />
      </div>
      <div className="flex gap-2">
        <Button type="submit" size="sm" disabled={alterar.isPending}>
          Salvar
        </Button>
        <Button type="button" size="sm" variant="ghost" onClick={aoTerminar}>
          Desistir
        </Button>
      </div>
      {alterar.error && <p className="w-full text-destructive text-sm">{alterar.error.message}</p>}
    </form>
  )
}

function NovoLembrete() {
  const criar = useCriarLembrete()
  const organizacoes = useOrganizacoes()
  const atual = useOrganizacaoAtual().organizacao
  const [texto, setTexto] = useState("")
  const [marcacao, setMarcacao] = useState(marcacaoVazia)
  const [organizacao, setOrganizacao] = useState(atual)
  return (
    <form
      className="flex flex-wrap items-end gap-3 rounded-md border p-3"
      onSubmit={(e) => {
        e.preventDefault()
        criar.mutate(
          { texto, quando: quandoDe(marcacao), precisao: marcacao.precisao, organizacao },
          {
            onSuccess: () => {
              setTexto("")
              setMarcacao({ ...marcacao, local: "" })
            },
          },
        )
      }}
    >
      <div className="min-w-48 flex-1 space-y-1.5">
        <Label htmlFor="texto">Lembrar de</Label>
        <Input id="texto" value={texto} onChange={(e) => setTexto(e.target.value)} required />
      </div>
      <CamposDeQuando prefixo="novo" marcacao={marcacao} mudar={setMarcacao} />
      <div className="w-36 space-y-1.5">
        <Label htmlFor="organizacao-lembrete">Organização</Label>
        <Input
          id="organizacao-lembrete"
          list="organizacoes-lembrete"
          value={organizacao}
          onChange={(e) => setOrganizacao(e.target.value)}
          required
        />
        <datalist id="organizacoes-lembrete">
          {(organizacoes.data ?? []).map((o) => (
            <option key={o.nome} value={o.nome} />
          ))}
        </datalist>
      </div>
      <Button type="submit" disabled={criar.isPending}>
        Agendar
      </Button>
      {criar.error && <p className="w-full text-destructive text-sm">{criar.error.message}</p>}
    </form>
  )
}
