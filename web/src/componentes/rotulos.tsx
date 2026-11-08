import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"
import { contornoDoTom, seloDoTom, type Tom } from "@/componentes/tons"

import type { EstadoDoRequisito, StatusLembrete, StatusSessao, StatusTarefa } from "@/api/cliente"

export const nomesStatusTarefa: Record<StatusTarefa, string> = {
  a_fazer: "A fazer",
  em_andamento: "Em andamento",
  pausada: "Pausada",
  bloqueada: "Bloqueada",
  em_validacao: "Em validação",
  concluida: "Concluída",
  cancelada: "Cancelada",
}

// Encerrada: concluída (entregue) ou cancelada (não queremos mais fazer). As demais estão abertas.
export const tarefaEncerrada = (status?: StatusTarefa) => status === "concluida" || status === "cancelada"

const tomDoStatusTarefa: Record<StatusTarefa, Tom> = {
  a_fazer: "neutro",
  em_andamento: "andamento",
  pausada: "atencao",
  bloqueada: "perigo",
  em_validacao: "validacao",
  concluida: "sucesso",
  cancelada: "neutro",
}

export function StatusDaTarefa({ status }: { status: StatusTarefa }) {
  return (
    <Badge
      variant="outline"
      className={cn("border-transparent", seloDoTom[tomDoStatusTarefa[status]], status === "cancelada" && "line-through")}
    >
      {nomesStatusTarefa[status]}
    </Badge>
  )
}

export const nomesStatusSessao: Record<StatusSessao, string> = {
  ativa: "Ativa",
  encerrada: "Encerrada",
  substituida: "Substituída",
}

export function StatusDaSessao({ status }: { status: StatusSessao }) {
  return (
    <Badge variant={status === "ativa" ? "default" : "secondary"}>{nomesStatusSessao[status]}</Badge>
  )
}

const formatoData = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" })

export function Data({ valor }: { valor?: string }) {
  if (!valor) return <span className="text-muted-foreground">—</span>
  return <time dateTime={valor}>{formatoData.format(new Date(valor))}</time>
}

export const nomesMotivoDeAtencao: Record<string, string> = {
  muitos_filhos: "Mais de 5 requisitos abaixo",
  sem_origem: "Sem origem",
  sem_descricao: "Sem descrição",
  sem_tarefa: "Ninguém trabalhando nele",
  trabalho_parado: "Trabalho parado",
  obsoleto_com_tarefa_aberta: "Obsoleto com tarefa aberta",
}

export const nomesEstadoDoRequisito: Record<EstadoDoRequisito, string> = {
  nao_atendido: "Não atendido",
  em_trabalho: "Em trabalho",
  nao_validado: "Não validado",
  validacao_expirada: "Validação expirada",
  validado: "Validado",
}

// Do menos ao mais maduro.
export const ordemDosEstados = Object.keys(nomesEstadoDoRequisito) as EstadoDoRequisito[]

const tomDoEstadoDoRequisito: Record<EstadoDoRequisito, Tom> = {
  em_trabalho: "andamento",
  nao_atendido: "perigo",
  nao_validado: "neutro",
  validado: "sucesso",
  validacao_expirada: "atencao",
}

// Estado calculado pelo servidor: o menos maduro entre o próprio (tarefas e última execução) e os de baixo.
export function EstadoDoRequisito({ estado, className }: { estado: EstadoDoRequisito; className?: string }) {
  return (
    <Badge variant="outline" className={cn("border-transparent", seloDoTom[tomDoEstadoDoRequisito[estado]], className)}>
      {nomesEstadoDoRequisito[estado]}
    </Badge>
  )
}

// Fração de 0 a 1 como percentual curto: "60%", "8,3%", "<0,1%".
export function percentual(f: number): string {
  if (f > 0 && f < 0.001) return "<0,1%"
  const p = f * 100
  return `${p >= 10 || Number.isInteger(p) ? Math.round(p) : p.toFixed(1).replace(".", ",")}%`
}

// Peso no total: a parte do requisito no trabalho todo (o zero vale 100%).
export function PesoNoTotal({ peso, className }: { peso: number; className?: string }) {
  return (
    <Badge variant="outline" className={cn(contornoDoTom.neutro, "tabular-nums", className)} title="Peso no total">
      {percentual(peso)} do total
    </Badge>
  )
}

export const nomesStatusLembrete: Record<StatusLembrete, string> = {
  agendado: "Agendado",
  adiado: "Adiado",
  disparado: "Tocando",
  confirmado: "Confirmado",
  cancelado: "Cancelado",
}

const tomDoStatusLembrete: Record<StatusLembrete, Tom> = {
  agendado: "neutro",
  adiado: "andamento",
  disparado: "perigo",
  confirmado: "sucesso",
  cancelado: "neutro",
}

export function StatusDoLembrete({ status }: { status: StatusLembrete }) {
  return (
    <Badge variant="outline" className={cn("border-transparent", seloDoTom[tomDoStatusLembrete[status]])}>
      {nomesStatusLembrete[status]}
    </Badge>
  )
}
