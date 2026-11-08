import { useMemo } from "react"
import { Link } from "react-router-dom"

import { type StatusTarefa, useTarefas } from "@/api/cliente"
import { Estado } from "@/componentes/Estado"
import { SelecaoMultipla } from "@/componentes/SelecaoMultipla"
import { useFiltrosLembrados } from "@/hooks/useFiltrosLembrados"
import { useOrganizacaoAtual } from "@/hooks/useOrganizacaoAtual"
import { Data, StatusDaTarefa, nomesStatusTarefa, percentual, tarefaEncerrada } from "@/componentes/rotulos"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"

const todosOsStatus = Object.keys(nomesStatusTarefa) as StatusTarefa[]
const abertas = todosOsStatus.filter((s) => !tarefaEncerrada(s))

// Filtros ficam na URL: o link reabre a mesma visão. Status e organização aceitam vários valores; sem
// filtro, todas. O padrão da primeira visita são as abertas.
export function Tarefas() {
  const { data, isLoading, error } = useTarefas()
  const { organizacao } = useOrganizacaoAtual()
  const [params, setParams] = useFiltrosLembrados("tarefas", `status=${abertas.join(",")}`)
  const texto = params.get("q") ?? ""
  const lista = (chave: string) => (params.get(chave) ?? "").split(",").filter(Boolean)
  const status = params.has("status") ? (lista("status") as StatusTarefa[]) : todosOsStatus
  // A fila segue o peso no total dos requisitos de cada tarefa; sem requisito, por último.
  const ordem = params.get("ordem") === "atualizacao" ? "atualizacao" : "peso"

  const alterar = (chave: string, valor: string | null) => {
    const novos = new URLSearchParams(params)
    if (valor === null) novos.delete(chave)
    else novos.set(chave, valor)
    setParams(novos, { replace: true })
  }

  const organizacoes = useMemo(() => [...new Set((data ?? []).map((t) => t.organizacao))].sort(), [data])
  // Sem "org" na URL, todas as organizações.
  const orgs = params.has("org") ? lista("org") : organizacoes
  const naOrganizacao = (o: string) => !(params.has("org") && !organizacao) || orgs.includes(o)

  const contagemStatus = useMemo(() => {
    const c = {} as Record<string, number>
    for (const t of data ?? []) if (naOrganizacao(t.organizacao)) c[t.status] = (c[t.status] ?? 0) + 1
    return c
  }, [data, params]) // eslint-disable-line react-hooks/exhaustive-deps
  const contagemOrg = useMemo(() => {
    const c = {} as Record<string, number>
    for (const t of data ?? []) if (status.includes(t.status)) c[t.organizacao] = (c[t.organizacao] ?? 0) + 1
    return c
  }, [data, params]) // eslint-disable-line react-hooks/exhaustive-deps

  const filtradas = useMemo(() => {
    const termos = texto.toLowerCase().split(/\s+/).filter(Boolean)
    return (data ?? [])
      .filter((t) => status.includes(t.status))
      .filter((t) => naOrganizacao(t.organizacao))
      .filter((t) => {
        const alvo = `${t.id} ${t.titulo} ${t.organizacao} ${t.descricao} ${t.resumo}`.toLowerCase()
        return termos.every((termo) => alvo.includes(termo))
      })
      .sort((a, b) =>
        ordem === "peso" ? (b.peso ?? 0) - (a.peso ?? 0) || b.atualizada_em.localeCompare(a.atualizada_em) : b.atualizada_em.localeCompare(a.atualizada_em),
      )
  }, [data, params]) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-2 sm:flex-row sm:flex-wrap">
        <SelecaoMultipla
          rotulo="Status"
          opcoes={todosOsStatus.map((s) => ({ valor: s, rotulo: nomesStatusTarefa[s], contagem: contagemStatus[s] ?? 0 }))}
          selecionados={status}
          alterar={(v) => alterar("status", v.length === todosOsStatus.length ? null : v.join(","))}
          atalhos={[
            { rotulo: "Abertas", valores: abertas },
            { rotulo: "Todos", valores: todosOsStatus },
          ]}
        />
        {!organizacao && (
        <SelecaoMultipla
          rotulo="Organização"
          opcoes={organizacoes.map((o) => ({ valor: o, rotulo: o, contagem: contagemOrg[o] ?? 0 }))}
          selecionados={orgs}
          alterar={(v) => alterar("org", v.length === organizacoes.length ? null : v.join(","))}
          atalhos={[{ rotulo: "Todas", valores: organizacoes }]}
        />
        )}
        <Select value={ordem} onValueChange={(v) => alterar("ordem", v === "peso" ? null : v)}>
          <SelectTrigger className="sm:w-56">
            <span className="text-muted-foreground">Ordem:</span>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="peso">Importância</SelectItem>
            <SelectItem value="atualizacao">Atualização</SelectItem>
          </SelectContent>
        </Select>
        <Input className="sm:w-56" placeholder="Buscar" value={texto} onChange={(e) => alterar("q", e.target.value || null)} />
        <Button asChild variant="outline">
          <Link to="/tarefas/nova">Nova tarefa</Link>
        </Button>
      </div>
      <p className="text-muted-foreground text-xs">
        {filtradas.length} de {data?.length ?? 0} tarefas ·{" "}
        <button type="button" className="underline" onClick={() => setParams(new URLSearchParams(), { replace: true })}>
          limpar filtros
        </button>
      </p>

      <Estado carregando={isLoading} erro={error} />
      {data && (
        <>
          <ul className="divide-y rounded-md border">
            {filtradas.map((t) => (
              <li key={t.id}>
                <Link to={`/tarefas/${t.id}`} className="block px-3 py-2.5 hover:bg-muted/50">
                  <div className="flex items-start justify-between gap-2">
                    <span className="font-medium">{t.titulo}</span>
                    <StatusDaTarefa status={t.status} />
                  </div>
                  <div className="mt-1 flex flex-wrap gap-x-3 text-muted-foreground text-xs">
                    <span className="font-mono">{t.id}</span>
                    <span>{t.organizacao}</span>
                    {t.peso !== undefined && <span className="tabular-nums" title="Peso no total dos requisitos da tarefa">{percentual(t.peso)} do total</span>}
                    {t.sessao_ativa && <span className="text-tom-andamento-fg">● {t.sessao_ativa}</span>}
                    {t.requisitos_obsoletos && (
                      <span className="text-tom-atencao-fg" title="Tenta atender requisitos obsoletos: conclua, ligue a outro requisito ou desvincule">
                        ⚠ requisito obsoleto
                      </span>
                    )}
                    <span>
                      atualizada <Data valor={t.atualizada_em} />
                    </span>
                  </div>
                </Link>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  )
}
