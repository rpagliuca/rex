import { useState } from "react"
import { Link, useParams } from "react-router-dom"

import { useAlterarComponente, useComponentes, useOrganizacoes, useRequisitos, useTarefas } from "@/api/cliente"
import { SelecaoMultipla } from "@/componentes/SelecaoMultipla"
import { Estado } from "@/componentes/Estado"
import { ForaDaOrganizacao } from "@/componentes/ForaDaOrganizacao"
import { EstadoDoRequisito, PesoNoTotal, StatusDaTarefa, tarefaEncerrada } from "@/componentes/rotulos"
import { Secao } from "@/componentes/Secao"
import { Texto } from "@/componentes/Texto"
import { Voltar } from "@/componentes/Voltar"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"

export function Componente() {
  const { id = "" } = useParams()
  const componentes = useComponentes()
  const requisitos = useRequisitos()
  const tarefas = useTarefas()
  const c = componentes.data?.find((x) => x.id === id)
  if (!c) return componentes.data ? <ForaDaOrganizacao /> : <Estado carregando={componentes.isLoading} erro={componentes.error} />
  const req = new Map((requisitos.data ?? []).map((r) => [r.id, r]))
  const tar = new Map((tarefas.data ?? []).map((t) => [t.id, t]))
  const reqs = c.requisitos.filter((r) => req.has(r))
  const tars = c.tarefas.filter((t) => tar.has(t)).sort((a, b) => Number(tarefaEncerrada(tar.get(a)!.status)) - Number(tarefaEncerrada(tar.get(b)!.status)))

  return (
    <div className="space-y-6">
      <header className="space-y-2">
        <Voltar para="/componentes" rotulo="Componentes" />
        <h1 className="font-semibold text-xl">{c.nome}</h1>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-muted-foreground text-sm">
          <span className="font-mono">{c.id}</span>
          <span>{c.organizacoes.join(", ")}</span>
          <Link to={`/requisitos?comp=${c.id}`} className="hover:underline">
            ver no grafo de requisitos
          </Link>
          <Editar id={c.id} nome={c.nome} descricao={c.descricao} organizacoes={c.organizacoes} />
        </div>
      </header>
      {c.descricao && (
        <Secao titulo="Descrição">
          <Texto>{c.descricao}</Texto>
        </Secao>
      )}
      <Secao titulo={`Requisitos (${reqs.length})`}>
        <ul className="divide-y rounded-md border">
          {reqs.map((rid) => {
            const r = req.get(rid)!
            return (
              <li key={rid}>
                <Link to={`/requisitos/${rid}`} className="flex items-start justify-between gap-2 px-3 py-2 hover:bg-muted/50">
                  <span className="text-sm">
                    <span className="font-mono text-muted-foreground text-xs">{rid}</span> {r.titulo}
                  </span>
                  <span className="flex shrink-0 gap-1">
                    <PesoNoTotal peso={r.peso} />
                    <EstadoDoRequisito estado={r.estado} />
                  </span>
                </Link>
              </li>
            )
          })}
        </ul>
      </Secao>
      <Secao titulo={`Tarefas (${tars.length})`}>
        <ul className="divide-y rounded-md border">
          {tars.map((tid) => {
            const t = tar.get(tid)!
            return (
              <li key={tid}>
                <Link to={`/tarefas/${tid}`} className="flex items-start justify-between gap-2 px-3 py-2 hover:bg-muted/50">
                  <span className="text-sm">
                    <span className="font-mono text-muted-foreground text-xs">{tid}</span> {t.titulo}
                  </span>
                  <StatusDaTarefa status={t.status} />
                </Link>
              </li>
            )
          })}
        </ul>
      </Secao>
    </div>
  )
}

function Editar({ id, nome, descricao, organizacoes }: { id: string; nome: string; descricao: string; organizacoes: string[] }) {
  const todas = useOrganizacoes()
  const alterar = useAlterarComponente(id)
  const [aberto, setAberto] = useState(false)
  const [n, setN] = useState(nome)
  const [d, setD] = useState(descricao)
  const [o, setO] = useState(organizacoes)
  return (
    <Dialog open={aberto} onOpenChange={setAberto}>
      <DialogTrigger asChild>
        <Button size="sm" variant="outline" className="h-7">
          Editar
        </Button>
      </DialogTrigger>
      <DialogContent>
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault()
            alterar.mutate({ nome: n, descricao: d, organizacoes: o }, { onSuccess: () => setAberto(false) })
          }}
        >
          <DialogHeader>
            <DialogTitle>Editar {id}</DialogTitle>
          </DialogHeader>
          <div className="space-y-1.5">
            <Label htmlFor="c-nome">Nome</Label>
            <Input id="c-nome" value={n} onChange={(e) => setN(e.target.value)} required />
          </div>
          <div className="space-y-1.5">
            <Label>Organizações</Label>
            <SelecaoMultipla
              rotulo="Organizações"
              opcoes={[...new Set([...(todas.data ?? []).filter((x) => x.zero).map((x) => x.nome), ...o])].map((x) => ({ valor: x, rotulo: x }))}
              selecionados={o}
              alterar={setO}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="c-desc">Descrição</Label>
            <Textarea id="c-desc" rows={6} value={d} onChange={(e) => setD(e.target.value)} />
          </div>
          {alterar.error && <p className="text-destructive text-sm">{alterar.error.message}</p>}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => setAberto(false)}>
              Cancelar
            </Button>
            <Button type="submit" disabled={alterar.isPending}>
              Salvar
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
