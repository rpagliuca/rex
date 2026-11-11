import { useState } from "react"
import { Building2, Plus } from "lucide-react"
import { Navigate, NavLink, Route, Routes, useLocation } from "react-router-dom"

import { NovaTarefa } from "@/paginas/NovaTarefa"
import { NovoRequisito } from "@/paginas/NovoRequisito"
import { Origens } from "@/paginas/Origens"
import { Requisito } from "@/paginas/Requisito"
import { Requisitos } from "@/paginas/Requisitos"
import { Sessao } from "@/paginas/Sessao"
import { Sessoes } from "@/paginas/Sessoes"
import { Componente } from "@/paginas/Componente"
import { Componentes } from "@/paginas/Componentes"
import { Assinatura, Logotipo } from "@/componentes/Logotipo"
import { Lembretes } from "@/paginas/Lembretes"
import { Template } from "@/paginas/Template"
import { Templates } from "@/paginas/Templates"
import { useLembretes, useOrganizacoes, useTemplates } from "@/api/cliente"
import { useOrganizacaoAtual } from "@/hooks/useOrganizacaoAtual"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Tarefa } from "@/paginas/Tarefa"
import { Tarefas } from "@/paginas/Tarefas"
import { cn } from "@/lib/utils"

const aba = ({ isActive }: { isActive: boolean }) =>
  cn("rounded-md px-1.5 py-1.5 text-sm sm:px-3", isActive ? "bg-muted font-medium" : "text-muted-foreground hover:text-foreground")

export default function App() {
  const templates = useTemplates()
  const devidos = (templates.data ?? []).filter((t) => t.devido).length
  const tocando = (useLembretes().data ?? []).filter((l) => l.status === "disparado").length
  return (
    <div className="mx-auto max-w-4xl px-4 py-4">
      {/* No celular a navbar cabe numa linha: só o símbolo, abas mais justas, Sessões no "+" e a organização como ícone. */}
      <nav className="mb-6 flex flex-wrap items-center gap-0.5 border-b pb-3 sm:gap-1">
        <NavLink to="/" className="mr-1 flex items-center sm:mr-3" aria-label="rex">
          <Logotipo className="h-7 w-auto sm:hidden" />
          <Assinatura className="hidden h-7 w-auto sm:block" />
        </NavLink>
        <NavLink to="/requisitos" className={aba}>
          Requisitos
        </NavLink>
        <NavLink to="/tarefas" end className={aba}>
          Tarefas
        </NavLink>
        <NavLink to="/lembretes" className={aba}>
          Lembretes
          {tocando > 0 && (
            <span className="ml-1.5 rounded-full bg-tom-perigo px-1.5 text-tom-perigo-fg text-xs font-medium" title={`${tocando} tocando agora`}>
              {tocando}
            </span>
          )}
        </NavLink>
        <NavLink to="/sessoes" className={(e) => cn(aba(e), "hidden sm:block")}>
          Sessões
        </NavLink>
        <MenuMais devidos={devidos} />
        <EscolherOrganizacao />
      </nav>
      <Routes>
        <Route path="/" element={<Navigate to="/requisitos" replace />} />
        <Route path="/tarefas" element={<Tarefas />} />
        <Route path="/tarefas/nova" element={<NovaTarefa />} />
        <Route path="/tarefas/:id" element={<Tarefa />} />
        <Route path="/requisitos" element={<Requisitos />} />
        <Route path="/requisitos/novo" element={<NovoRequisito />} />
        <Route path="/requisitos/:id" element={<Requisito />} />
        <Route path="/origens" element={<Origens />} />
        <Route path="/templates" element={<Templates />} />
        <Route path="/templates/:id" element={<Template />} />
        <Route path="/lembretes" element={<Lembretes />} />
        <Route path="/lembretes/:id" element={<Lembretes />} />
        <Route path="/componentes" element={<Componentes />} />
        <Route path="/componentes/:id" element={<Componente />} />
        <Route path="/sessoes" element={<Sessoes />} />
        <Route path="/sessoes/:id" element={<Sessao />} />
      </Routes>
    </div>
  )
}

// Telas de cadastro, usadas de vez em quando: ficam atrás do "+" para não ocupar a navbar.
function MenuMais({ devidos }: { devidos: number }) {
  const [aberto, setAberto] = useState(false)
  const { pathname } = useLocation()
  const ativo = ["/templates", "/componentes", "/origens"].some((r) => pathname.startsWith(r))
  const item = ({ isActive }: { isActive: boolean }) =>
    cn("flex items-center rounded-md px-3 py-1.5 text-sm", isActive ? "bg-muted font-medium" : "text-muted-foreground hover:bg-muted hover:text-foreground")
  return (
    <Popover open={aberto} onOpenChange={setAberto}>
      <PopoverTrigger
        aria-label="Mais telas"
        title="Tarefas Pré-moldadas, Componentes, Origens"
        className={cn("relative rounded-md px-2 py-1.5", ativo ? "bg-muted text-foreground" : "text-muted-foreground hover:text-foreground")}
      >
        <Plus className="h-4 w-4" />
        {devidos > 0 && (
          <span className="absolute -top-0.5 -right-0.5 h-2 w-2 rounded-full bg-tom-atencao" title={`${devidos} pré-moldadas devidas agora`} />
        )}
      </PopoverTrigger>
      <PopoverContent align="start" className="flex w-56 flex-col gap-0.5 p-1">
        <NavLink to="/sessoes" className={(e) => cn(item(e), "sm:hidden")} onClick={() => setAberto(false)}>
          Sessões
        </NavLink>
        <NavLink to="/templates" className={item} onClick={() => setAberto(false)}>
          Tarefas Pré-moldadas
          {devidos > 0 && (
            <span className="ml-auto rounded-full bg-tom-atencao px-1.5 text-tom-atencao-fg text-xs font-medium" title={`${devidos} devidas agora`}>
              {devidos}
            </span>
          )}
        </NavLink>
        <NavLink to="/componentes" className={item} onClick={() => setAberto(false)}>
          Componentes
        </NavLink>
        <NavLink to="/origens" className={item} onClick={() => setAberto(false)}>
          Origens
        </NavLink>
      </PopoverContent>
    </Popover>
  )
}

// Escolha global: com uma organização, todas as telas mostram só o que é dela (bom para compartilhar a tela).
function EscolherOrganizacao() {
  const { organizacao, escolher } = useOrganizacaoAtual()
  const organizacoes = useOrganizacoes()
  const comZero = (organizacoes.data ?? []).filter((o) => o.zero || o.nome === organizacao)
  return (
    <Select value={organizacao || "_todas"} onValueChange={(v) => escolher(v === "_todas" ? "" : v)}>
      <SelectTrigger
        aria-label="Organização"
        title={organizacao || "Todas as organizações"}
        className={cn("ml-auto h-8 w-auto gap-2 px-2 sm:px-3 [&>svg:last-child]:hidden sm:[&>svg:last-child]:block", organizacao && "border-primary font-medium")}
      >
        <Building2 className={cn("sm:hidden", organizacao && "text-primary")} />
        <span className="hidden sm:contents">
          <SelectValue />
        </span>
      </SelectTrigger>
      <SelectContent align="end">
        <SelectItem value="_todas">Todas as organizações</SelectItem>
        {(comZero.length > 0 ? comZero : organizacoes.data ?? []).map((o) => (
          <SelectItem key={o.nome} value={o.nome}>
            {o.nome}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
