import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { useOrganizacaoAtual } from "@/hooks/useOrganizacaoAtual"

import type { components } from "./esquema"

export type Tarefa = components["schemas"]["Tarefa"]
export type Sessao = components["schemas"]["Sessao"]
export type Evento = components["schemas"]["Evento"]
export type StatusTarefa = components["schemas"]["StatusTarefa"]
export type StatusSessao = components["schemas"]["StatusSessao"]
export type NovaTarefa = components["schemas"]["NovaTarefa"]
export type AlteracaoTarefa = components["schemas"]["AlteracaoTarefa"]
export type Requisito = components["schemas"]["Requisito"]
export type Origem = components["schemas"]["Origem"]
export type NovoRequisito = components["schemas"]["NovoRequisito"]
export type AlteracaoRequisito = components["schemas"]["AlteracaoRequisito"]
export type EstadoDoRequisito = components["schemas"]["EstadoDoRequisito"]
export type Execucao = components["schemas"]["Execucao"]
export type NovaExecucao = components["schemas"]["NovaExecucao"]
export type Template = components["schemas"]["Template"]
export type NovoTemplate = components["schemas"]["NovoTemplate"]
export type AlteracaoTemplate = components["schemas"]["AlteracaoTemplate"]
export type Organizacao = components["schemas"]["Organizacao"]
export type Componente = components["schemas"]["Componente"]
export type NovaOrigem = components["schemas"]["NovaOrigem"]
export type PedidoDeLancamento = components["schemas"]["PedidoDeLancamento"]
export type OpcoesDeLancamento = components["schemas"]["OpcoesDeLancamento"]
export type Lembrete = components["schemas"]["Lembrete"]
export type StatusLembrete = components["schemas"]["StatusLembrete"]
export type PrecisaoLembrete = components["schemas"]["PrecisaoLembrete"]
export type NovoLembrete = components["schemas"]["NovoLembrete"]
export type AlteracaoLembrete = components["schemas"]["AlteracaoLembrete"]
export type Adiamento = components["schemas"]["Adiamento"]
export type Recursos = components["schemas"]["Recursos"]

// ErroDaApi leva, além da mensagem, a medição da máquina quando o lançamento foi recusado por falta
// de memória.
export class ErroDaApi extends Error {
  recursos?: Recursos
  constructor(mensagem: string, recursos?: Recursos) {
    super(mensagem)
    this.recursos = recursos
  }
}

// A API fica na mesma origem do frontend; em desenvolvimento, o Vite encaminha.
async function obter<T>(caminho: string, metodo = "GET", corpo?: unknown): Promise<T> {
  const resposta = await fetch(caminho, {
    method: metodo,
    headers: corpo === undefined ? undefined : { "Content-Type": "application/json" },
    body: corpo === undefined ? undefined : JSON.stringify(corpo),
  })
  // No endereço com login, o cookie venceu: a navegação refaz o login pelo Google.
  if (resposta.status === 401 && resposta.headers.get("X-Rex-Login") === "necessario") {
    window.location.assign("/login/google")
  }
  if (!resposta.ok) {
    const corpo = await resposta.json().catch(() => null)
    throw new ErroDaApi(corpo?.mensagem ?? `a API respondeu ${resposta.status}`, corpo?.recursos)
  }
  return resposta.json()
}

// Com uma organização escolhida na barra superior, as listas só trazem o que é dela.
const daOrganizacao =
  <T extends { organizacao: string }>(org: string) =>
  (itens: T[]) =>
    org ? itens.filter((x) => x.organizacao === org) : itens

export const useTarefas = () => {
  const { organizacao } = useOrganizacaoAtual()
  return useQuery({ queryKey: ["tarefas"], queryFn: () => obter<Tarefa[]>("/tarefas"), select: daOrganizacao<Tarefa>(organizacao) })
}

// Telas de detalhe se atualizam sozinhas: o lançador e os agentes mudam o estado por fora.
const atualizacao = 5_000

export const useTarefa = (id: string) =>
  useQuery({ queryKey: ["tarefa", id], queryFn: () => obter<Tarefa>(`/tarefas/${id}`), refetchInterval: atualizacao })

export const useSessoesDaTarefa = (id: string) =>
  useQuery({
    queryKey: ["tarefa", id, "sessoes"],
    queryFn: () => obter<Sessao[]>(`/tarefas/${id}/sessoes`),
    refetchInterval: atualizacao,
  })

// Uma sessão é da organização quando todas as tarefas dela são; sem tarefa, só na visão geral.
export const useSessoes = () => {
  const { organizacao } = useOrganizacaoAtual()
  const tarefas = useTarefas()
  const sessoes = useQuery({ queryKey: ["sessoes"], queryFn: () => obter<Sessao[]>("/sessoes"), refetchInterval: atualizacao })
  if (!organizacao) return sessoes
  const minhas = new Set((tarefas.data ?? []).map((t) => t.id))
  const data = sessoes.data?.filter((x) => x.tarefas.length > 0 && x.tarefas.every((t) => minhas.has(t)))
  return { ...sessoes, data: tarefas.data ? data : undefined }
}

export const useSessao = (id: string) =>
  useQuery({ queryKey: ["sessao", id], queryFn: () => obter<Sessao>(`/sessoes/${id}`), refetchInterval: atualizacao })

// Depois de qualquer escrita, tudo é recarregado: são poucas consultas e o estado se espalha.
function useEscrita<E, R>(executar: (entrada: E) => Promise<R>) {
  const cliente = useQueryClient()
  return useMutation({ mutationFn: executar, onSuccess: () => cliente.invalidateQueries() })
}

export const useAlterarTarefa = (id: string) =>
  useEscrita((alteracao: AlteracaoTarefa) => obter<Tarefa>(`/tarefas/${id}`, "PATCH", alteracao))

export const useCriarTarefa = () => useEscrita((nova: NovaTarefa) => obter<Tarefa>("/tarefas", "POST", nova))

export const useEncerrarSessao = (id: string) =>
  useEscrita((resumo: string) => obter<Sessao>(`/sessoes/${id}/encerramento-manual`, "POST", { resumo }))

export const useEventos = (entidade: string) =>
  useQuery({ queryKey: ["eventos", entidade], queryFn: () => obter<Evento[]>(`/eventos?entidade=${entidade}`) })

// Sem lançador configurado, a API responde 404 e o frontend só muda o status.
export const useOpcoesDoLancador = () =>
  useQuery({
    queryKey: ["lancador"],
    queryFn: () => obter<OpcoesDeLancamento>("/lancador").catch(() => null),
    staleTime: Infinity,
  })

export const useLancarAgente = (id: string) =>
  useEscrita((pedido: PedidoDeLancamento) => obter<Tarefa>(`/tarefas/${id}/lancamento`, "POST", pedido))

export const useRequisitos = () => {
  const { organizacao } = useOrganizacaoAtual()
  return useQuery({
    queryKey: ["requisitos"],
    queryFn: () => obter<Requisito[]>("/requisitos"),
    refetchInterval: atualizacao,
    select: daOrganizacao<Requisito>(organizacao),
  })
}

export const useOrganizacoes = () =>
  useQuery({ queryKey: ["organizacoes"], queryFn: () => obter<Organizacao[]>("/organizacoes") })

export const useDefinirZero = (nome: string) =>
  useEscrita((zero: string) => obter<Organizacao>(`/organizacoes/${encodeURIComponent(nome)}`, "PUT", { zero }))

// O zero da visão atual: o da organização escolhida ou, na visão geral, o único sem pais.
export const useZero = () => {
  const { organizacao } = useOrganizacaoAtual()
  const organizacoes = useOrganizacoes()
  const requisitos = useRequisitos()
  if (organizacao) return organizacoes.data?.find((o) => o.nome === organizacao)?.zero
  return requisitos.data?.find((r) => r.deriva_de.length === 0)?.id
}

export const useOrigens = () => useQuery({ queryKey: ["origens"], queryFn: () => obter<Origem[]>("/origens") })

export const useCriarRequisito = () =>
  useEscrita((novo: NovoRequisito) => obter<Requisito>("/requisitos", "POST", novo))

export const useAlterarRequisito = (id: string) =>
  useEscrita((alteracao: AlteracaoRequisito) => obter<Requisito>(`/requisitos/${id}`, "PATCH", alteracao))

export const useDerivar = (id: string) =>
  useEscrita((pai: string) => obter<Requisito>(`/requisitos/${id}/derivacoes`, "POST", { pai }))

// Pontos de importância do requisito sob um pai; null volta a não triado.
export const useDefinirPontos = (id: string) =>
  useEscrita(({ pai, pontos }: { pai: string; pontos: number | null }) =>
    obter<Requisito>(`/requisitos/${id}/derivacoes/${pai}`, "PUT", pontos === null ? {} : { pontos }),
  )

// Obsoleto: fica no histórico, com o motivo, mas sai do cálculo dos pais; reativar desfaz o engano.
export const useMarcarObsoleto = (id: string) =>
  useEscrita((motivo: string) => obter<Requisito>(`/requisitos/${id}/obsolescencia`, "PUT", { motivo }))

export const useReativarRequisito = (id: string) =>
  useEscrita<void, Requisito>(() => obter<Requisito>(`/requisitos/${id}/obsolescencia`, "DELETE"))

export const useDesfazerDerivacao = (id: string) =>
  useEscrita((pai: string) => obter<Requisito>(`/requisitos/${id}/derivacoes/${pai}`, "DELETE"))

export const useExecucoes = (id: string) =>
  useQuery({ queryKey: ["execucoes", id], queryFn: () => obter<Execucao[]>(`/requisitos/${id}/execucoes`) })

export const useRegistrarExecucao = (id: string) =>
  useEscrita((nova: NovaExecucao) => obter<Execucao>(`/requisitos/${id}/execucoes`, "POST", nova))

export const useCriarOrigem = () => useEscrita((nova: NovaOrigem) => obter<Origem>("/origens", "POST", nova))

export const useVincularRequisito = (tarefa: string) =>
  useEscrita((requisito: string) => obter<Tarefa>(`/tarefas/${tarefa}/requisitos`, "POST", { requisito }))

export const useDesvincularRequisito = (tarefa: string) =>
  useEscrita((requisito: string) => obter<Tarefa>(`/tarefas/${tarefa}/requisitos/${requisito}`, "DELETE"))

export const useTemplates = () => {
  const { organizacao } = useOrganizacaoAtual()
  return useQuery({
    queryKey: ["templates"],
    queryFn: () => obter<Template[]>("/templates"),
    refetchInterval: atualizacao,
    select: daOrganizacao<Template>(organizacao),
  })
}

export const useCriarTemplate = () => useEscrita((novo: NovoTemplate) => obter<Template>("/templates", "POST", novo))

export const useAlterarTemplate = (id: string) =>
  useEscrita((alteracao: AlteracaoTemplate) => obter<Template>(`/templates/${id}`, "PATCH", alteracao))

export const useInstanciarTemplate = (id: string) =>
  useEscrita((contexto?: string) => obter<Tarefa>(`/templates/${id}/instancias`, "POST", { contexto }))

export const useComponentes = () => {
  const { organizacao } = useOrganizacaoAtual()
  return useQuery({
    queryKey: ["componentes"],
    queryFn: () => obter<Componente[]>("/componentes"),
    select: (itens: Componente[]) => (organizacao ? itens.filter((c) => c.organizacoes.includes(organizacao)) : itens),
  })
}

export const useCriarComponente = () =>
  useEscrita((novo: { nome: string; organizacoes: string[]; descricao?: string }) => obter<Componente>("/componentes", "POST", novo))

export const useAlterarComponente = (id: string) =>
  useEscrita((alteracao: { nome?: string; organizacoes?: string[]; descricao?: string }) => obter<Componente>(`/componentes/${id}`, "PATCH", alteracao))

// alvo: "requisitos" ou "tarefas".
export const useVincularComponente = (alvo: string, id: string) =>
  useEscrita((componente: string) => obter<unknown>(`/${alvo}/${id}/componentes`, "POST", { componente }))

export const useDesvincularComponente = (alvo: string, id: string) =>
  useEscrita((componente: string) => obter<unknown>(`/${alvo}/${id}/componentes/${componente}`, "DELETE"))

// Sem status, a API traz só os que pedem atenção (agendados, adiados e disparados).
export const useLembretes = (status?: StatusLembrete[]) => {
  const { organizacao } = useOrganizacaoAtual()
  const filtro = status ? `?status=${status.join(",")}` : ""
  return useQuery({
    queryKey: ["lembretes", filtro],
    queryFn: () => obter<Lembrete[]>(`/lembretes${filtro}`),
    refetchInterval: atualizacao,
    select: daOrganizacao<Lembrete>(organizacao),
  })
}

export const useCriarLembrete = () => useEscrita((novo: NovoLembrete) => obter<Lembrete>("/lembretes", "POST", novo))

export const useAlterarLembrete = () =>
  useEscrita(({ id, ...alteracao }: AlteracaoLembrete & { id: string }) => obter<Lembrete>(`/lembretes/${id}`, "PATCH", alteracao))

export const useConfirmarLembrete = () =>
  useEscrita((id: string) => obter<Lembrete>(`/lembretes/${id}/confirmacao`, "POST"))

export const useCancelarLembrete = () =>
  useEscrita((id: string) => obter<Lembrete>(`/lembretes/${id}/cancelamento`, "POST"))

export const useAdiarLembrete = () =>
  useEscrita(({ id, ...adiamento }: Adiamento & { id: string }) => obter<Lembrete>(`/lembretes/${id}/adiamento`, "POST", adiamento))
