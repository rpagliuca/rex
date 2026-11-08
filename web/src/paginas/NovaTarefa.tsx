import { useMemo, useState } from "react"
import { Link, useNavigate } from "react-router-dom"

import { type StatusTarefa, useCriarTarefa, useTarefas } from "@/api/cliente"
import { nomesStatusTarefa } from "@/componentes/rotulos"
import { useOrganizacaoAtual } from "@/hooks/useOrganizacaoAtual"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"

export function NovaTarefa() {
  const navegar = useNavigate()
  const criar = useCriarTarefa()
  const tarefas = useTarefas()
  const organizacoes = useMemo(() => [...new Set((tarefas.data ?? []).map((t) => t.organizacao))].sort(), [tarefas.data])
  const [titulo, setTitulo] = useState("")
  const atual = useOrganizacaoAtual().organizacao
  const [organizacao, setOrganizacao] = useState(atual)
  const [descricao, setDescricao] = useState("")
  const [status, setStatus] = useState<StatusTarefa>("a_fazer")

  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault()
        criar.mutate({ titulo, organizacao, descricao, status }, { onSuccess: (t) => navegar(`/tarefas/${t.id}`) })
      }}
    >
      <Link to="/tarefas" className="text-muted-foreground text-sm hover:underline">
        ← Tarefas
      </Link>
      <h1 className="font-semibold text-xl">Nova tarefa</h1>
      <div className="space-y-1.5">
        <Label htmlFor="titulo">Título</Label>
        <Input id="titulo" value={titulo} onChange={(e) => setTitulo(e.target.value)} required />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="organizacao">Organização</Label>
        <Input
          id="organizacao"
          list="organizacoes"
          value={organizacao}
          onChange={(e) => setOrganizacao(e.target.value)}
          required
        />
        <datalist id="organizacoes">
          {organizacoes.map((o) => (
            <option key={o} value={o} />
          ))}
        </datalist>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="descricao">Descrição</Label>
        <Textarea id="descricao" rows={8} value={descricao} onChange={(e) => setDescricao(e.target.value)} />
      </div>
      <div className="space-y-1.5">
        <Label>Status</Label>
        <Select value={status} onValueChange={(v) => setStatus(v as StatusTarefa)}>
          <SelectTrigger className="w-48">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {(Object.keys(nomesStatusTarefa) as StatusTarefa[])
              .filter((s) => s !== "em_andamento" && s !== "cancelada")
              .map((s) => (
                <SelectItem key={s} value={s}>
                  {nomesStatusTarefa[s]}
                </SelectItem>
              ))}
          </SelectContent>
        </Select>
        <p className="text-muted-foreground text-xs">Para pôr um agente nela, use "Iniciar agente…" na página da tarefa.</p>
      </div>
      <Button type="submit" disabled={criar.isPending}>
        Criar
      </Button>
      {criar.error && <p className="text-destructive text-sm">{criar.error.message}</p>}
    </form>
  )
}
