import { Check, ChevronDown, ChevronRight, ExternalLink, Pencil, X } from "lucide-react"
import { type KeyboardEvent, useState } from "react"
import { Link } from "react-router-dom"

import { type Requisito, type Tarefa, useAlterarRequisito, useDefinirPontos, useTarefas } from "@/api/cliente"
import { EstadoDoRequisito, PesoNoTotal, percentual, StatusDaTarefa, tarefaEncerrada } from "@/componentes/rotulos"
import { descendentes } from "@/componentes/trilhas"
import { cn } from "@/lib/utils"

// Recorte do grafo: os requisitos que casam com o filtro e os ancestrais deles até o zero.
export type Recorte = { casam: Set<string>; visiveis: Set<string> }

export function recortar(porId: Map<string, Requisito>, casa: (r: Requisito) => boolean): Recorte {
  const casam = new Set([...porId.values()].filter(casa).map((r) => r.id))
  const visiveis = new Set<string>()
  const subir = (id: string) => {
    if (visiveis.has(id)) return
    visiveis.add(id)
    for (const p of porId.get(id)?.deriva_de ?? []) subir(p)
  }
  casam.forEach(subir)
  return { casam, visiveis }
}

// Navegação pelo grafo, sempre colapsado de início: clicar num requisito abre as tarefas dele (abertas
// primeiro) e os que derivam dele.
// Os irmãos vêm do mais ao menos importante; sob um pai, cada um mostra os pontos e a fatia entre os
// irmãos, e todos mostram o peso no total. O lápis (só no hover, como o link) abre a própria linha
// para editar título e pontos.
// Com recorte, só aparecem os do recorte. Um requisito com vários pais aparece sob cada um; a chave
// do nó é o caminho até ele.
export function GrafoDeRequisitos({
  raizes,
  porId,
  recorte,
  memoria,
  pai,
}: {
  raizes: string[]
  // O pai das raízes, quando elas são os derivados de um requisito: mostra os pontos sob ele.
  pai?: string
  porId: Map<string, Requisito>
  recorte?: Recorte
  // Com memória, os nós abertos sobrevivem à ida a uma tarefa e à volta.
  memoria?: string
}) {
  const [abertos, setAbertos] = useState<Set<string>>(() => new Set(lerAbertos(memoria)))
  const tarefas = useTarefas()
  const tarefasPorId = new Map((tarefas.data ?? []).map((t) => [t.id, t]))
  const alternar = (chave: string) =>
    setAbertos((atual) => {
      const novo = new Set(atual)
      if (novo.has(chave)) novo.delete(chave)
      else novo.add(chave)
      gravarAbertos(memoria, novo)
      return novo
    })
  const aberto = (chave: string) => abertos.has(chave)
  const mostradas = ordenarPorFatia(recorte ? raizes.filter((id) => recorte.visiveis.has(id)) : raizes, pai, porId)
  if (mostradas.length === 0) return <p className="text-muted-foreground text-sm">Nenhum.</p>
  return (
    <ul className="space-y-0.5">
      {mostradas.map((id) => (
        <No key={id} id={id} pai={pai} chave={id} porId={porId} tarefasPorId={tarefasPorId} aberto={aberto} alternar={alternar} recorte={recorte} />
      ))}
    </ul>
  )
}

function lerAbertos(memoria?: string): string[] {
  if (!memoria) return []
  try {
    return JSON.parse(sessionStorage.getItem(`grafo:${memoria}`) ?? "[]")
  } catch {
    return []
  }
}

function gravarAbertos(memoria: string | undefined, abertos: Set<string>) {
  if (!memoria) return
  try {
    sessionStorage.setItem(`grafo:${memoria}`, JSON.stringify([...abertos]))
  } catch {
    // sem armazenamento, o grafo só volta colapsado
  }
}

const fatiaSob = (r: Requisito | undefined, pai?: string) => r?.importancia.find((i) => i.pai === pai)?.fatia ?? 0

// Do mais ao menos importante sob o pai; sem pai (as raízes), pelo peso no total.
function ordenarPorFatia(ids: string[], pai: string | undefined, porId: Map<string, Requisito>) {
  const chave = (id: string) => (pai ? fatiaSob(porId.get(id), pai) : (porId.get(id)?.peso ?? 0))
  return [...ids].sort((a, b) => chave(b) - chave(a))
}

const abertaPrimeiro = (t?: Tarefa) => (tarefaEncerrada(t?.status) ? 1 : 0)

function No({
  id,
  pai,
  chave,
  porId,
  tarefasPorId,
  aberto,
  alternar,
  recorte,
}: {
  id: string
  pai?: string
  chave: string
  porId: Map<string, Requisito>
  tarefasPorId: Map<string, Tarefa>
  aberto: (chave: string) => boolean
  alternar: (chave: string) => void
  recorte?: Recorte
}) {
  const [editando, setEditando] = useState(false)
  const r = porId.get(id)
  if (!r) return null
  const filhos = ordenarPorFatia(
    (recorte ? r.derivados.filter((f) => recorte.visiveis.has(f)) : r.derivados).filter((f) => porId.has(f)),
    id,
    porId,
  )
  const estaAberto = aberto(chave)
  const tarefas = r.tarefas.filter((t) => tarefasPorId.has(t)).sort((a, b) => abertaPrimeiro(tarefasPorId.get(a)) - abertaPrimeiro(tarefasPorId.get(b)))
  const abre = filhos.length > 0 || tarefas.length > 0
  const abaixo = r.derivados.length > 0 ? descendentes(id, porId) : new Set<string>()
  const emTrabalhoAbaixo = [...abaixo].filter((x) => porId.get(x)?.estado === "em_trabalho").length
  const atencaoAbaixo = [...abaixo].filter((x) => (porId.get(x)?.atencao.length ?? 0) > 0).length
  const soCaminho = recorte && !recorte.casam.has(id)
  const ligacao = pai ? r.importancia.find((i) => i.pai === pai) : undefined

  return (
    <li>
      {editando ? (
        <EdicaoDaLinha requisito={r} pai={ligacao && pai} fechar={() => setEditando(false)} />
      ) : (
        <div className="group flex items-start gap-1 rounded-md hover:bg-muted/60">
          <button
            type="button"
            className={cn("flex flex-1 items-start gap-1.5 px-1.5 py-1.5 text-left text-sm disabled:cursor-default", (soCaminho || r.obsoleto) && "opacity-60")}
            onClick={() => abre && alternar(chave)}
            disabled={!abre}
          >
            <span className="mt-0.5 w-4 shrink-0 text-muted-foreground">
              {abre && (estaAberto ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />)}
            </span>
            <span className="flex-1">
              <span className={cn(r.obsoleto && "line-through")}>{r.titulo}</span>
              <span className="ml-2 inline-flex flex-wrap items-center gap-x-2 text-muted-foreground text-xs">
                {r.obsoleto && <span title={r.obsoleto.motivo}>obsoleto</span>}
                <EstadoDoRequisito estado={r.estado} className="h-5 px-1.5 text-[11px]" />
                <PesoNoTotal peso={r.peso} className="h-5 px-1.5 text-[11px]" />
                {filhos.length > 0 && <span>{recorte ? [...abaixo].filter((x) => recorte.casam.has(x)).length : abaixo.size} abaixo</span>}
                {r.tarefas.length > 0 && <span>{r.tarefas.length} tarefas</span>}
                {!recorte && emTrabalhoAbaixo > 0 && <span className="text-tom-andamento-fg">● {emTrabalhoAbaixo} em trabalho abaixo</span>}
                {r.atencao.length > 0 && (
                  <span className="text-tom-atencao-fg" title={r.atencao.map((a) => a.detalhe).join("\n")}>
                    ⚠ {r.atencao.length}
                  </span>
                )}
                {!recorte && atencaoAbaixo > 0 && <span className="text-tom-atencao-fg/80">{atencaoAbaixo} abaixo pedem atenção</span>}
              </span>
            </span>
          </button>
          {ligacao && (
            <span className="py-1.5 text-muted-foreground text-xs tabular-nums" title="Pontos entre os irmãos e a fatia deles">
              {ligacao.pontos ?? "?"} pts · {percentual(ligacao.fatia)}
            </span>
          )}
          <span className="flex opacity-0 group-focus-within:opacity-100 group-hover:opacity-100 pointer-coarse:opacity-100">
            <button
              type="button"
              className="px-1.5 py-1.5 text-muted-foreground hover:text-foreground"
              onClick={() => setEditando(true)}
              title={`Editar ${id} nesta linha`}
              aria-label={`Editar ${id} nesta linha`}
            >
              <Pencil className="size-4" />
            </button>
            <Link
              to={`/requisitos/${id}`}
              className="px-2 py-1.5 text-muted-foreground hover:text-foreground"
              title={`Abrir ${id}`}
              aria-label={`Abrir ${id}`}
            >
              <ExternalLink className="size-4" />
            </Link>
          </span>
        </div>
      )}
      {estaAberto && abre && (
        <ul className="ml-3.5 space-y-0.5 border-l pl-1.5">
          {tarefas.map((tid) => {
            const t = tarefasPorId.get(tid)
            return (
              <li key={tid}>
                <Link
                  to={`/tarefas/${tid}`}
                  className={cn("flex items-start gap-2 rounded-md px-1.5 py-1 pl-6 text-sm hover:bg-muted/60", tarefaEncerrada(t?.status) && "opacity-60")}
                >
                  <span className="flex-1">
                    <span className="mr-1.5 font-mono text-muted-foreground text-xs">{tid}</span>
                    {t?.titulo}
                  </span>
                  {t && <StatusDaTarefa status={t.status} />}
                </Link>
              </li>
            )
          })}
          {filhos.map((f) => (
            <No key={f} id={f} pai={id} chave={`${chave}>${f}`} porId={porId} tarefasPorId={tarefasPorId} aberto={aberto} alternar={alternar} recorte={recorte} />
          ))}
        </ul>
      )}
    </li>
  )
}

// A linha aberta para edição: título e, sob um pai, os pontos (vazio volta a não triado). Enter grava,
// Esc cancela; só vai para a API o que mudou.
function EdicaoDaLinha({ requisito: r, pai, fechar }: { requisito: Requisito; pai?: string; fechar: () => void }) {
  const alterar = useAlterarRequisito(r.id)
  const definir = useDefinirPontos(r.id)
  const pontosAtuais = (pai && r.importancia.find((i) => i.pai === pai)?.pontos?.toString()) || ""
  const [titulo, setTitulo] = useState(r.titulo)
  const [pontos, setPontos] = useState(pontosAtuais)
  const [erro, setErro] = useState("")
  const gravando = alterar.isPending || definir.isPending
  const gravar = async () => {
    const novoTitulo = titulo.trim()
    const textoPontos = pontos.trim()
    const novosPontos = textoPontos === "" ? null : Number(textoPontos)
    if (!novoTitulo) return setErro("O título não pode ficar vazio")
    if (novosPontos !== null && (!Number.isInteger(novosPontos) || novosPontos < 1)) return setErro("Pontos: inteiro a partir de 1, ou vazio")
    try {
      if (novoTitulo !== r.titulo) await alterar.mutateAsync({ titulo: novoTitulo })
      if (pai && textoPontos !== pontosAtuais) await definir.mutateAsync({ pai, pontos: novosPontos })
      fechar()
    } catch (e) {
      setErro(e instanceof Error ? e.message : String(e))
    }
  }
  const teclas = (e: KeyboardEvent) => {
    if (e.key === "Enter") {
      e.preventDefault()
      gravar()
    } else if (e.key === "Escape") fechar()
  }
  return (
    <div className="rounded-md bg-muted/60 px-1.5 py-1">
      <div className="flex items-center gap-1.5 pl-5.5">
        <input
          autoFocus
          aria-label={`Título de ${r.id}`}
          className="h-7 min-w-0 flex-1 rounded border bg-background px-1.5 text-sm"
          value={titulo}
          onChange={(e) => setTitulo(e.target.value)}
          onKeyDown={teclas}
        />
        {pai && (
          <input
            type="number"
            min={1}
            inputMode="numeric"
            aria-label={`Pontos de ${r.id} sob ${pai}`}
            title="Pontos entre os irmãos; vazio: não triado (conta como 1)"
            placeholder="?"
            className={cn("h-7 w-14 rounded border bg-background px-1 text-right text-sm tabular-nums", !pontosAtuais && "border-dashed")}
            value={pontos}
            onChange={(e) => setPontos(e.target.value)}
            onKeyDown={teclas}
          />
        )}
        <button
          type="button"
          className="px-1.5 py-1 text-muted-foreground hover:text-foreground disabled:opacity-50"
          onClick={gravar}
          disabled={gravando}
          title="Gravar (Enter)"
          aria-label="Gravar"
        >
          <Check className="size-4" />
        </button>
        <button type="button" className="px-1.5 py-1 text-muted-foreground hover:text-foreground" onClick={fechar} title="Cancelar (Esc)" aria-label="Cancelar">
          <X className="size-4" />
        </button>
      </div>
      {erro && <p className="pt-1 pl-5.5 text-destructive text-xs">{erro}</p>}
    </div>
  )
}
