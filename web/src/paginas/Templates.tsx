import { useMemo, useState } from "react"
import { Link, useNavigate } from "react-router-dom"

import { type Template, useCriarTemplate, useTemplates } from "@/api/cliente"
import { Estado } from "@/componentes/Estado"
import { FormularioDeTemplate, Periodicidade } from "@/componentes/FormularioDeTemplate"
import { Data } from "@/componentes/rotulos"
import { SelecaoMultipla } from "@/componentes/SelecaoMultipla"
import { Filtros } from "@/componentes/Filtros"
import { useFiltrosLembrados } from "@/hooks/useFiltrosLembrados"
import { useOrganizacaoAtual } from "@/hooks/useOrganizacaoAtual"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogTrigger } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"

const tipos = [
  { valor: "devido", rotulo: "Devidas agora" },
  { valor: "periodico", rotulo: "Periódicas em dia" },
  { valor: "sob_demanda", rotulo: "Sob demanda" },
]
const tipoDe = (t: Template) => (t.devido ? "devido" : t.periodicidade_dias ? "periodico" : "sob_demanda")

// Tarefas pré-moldadas (templates): sempre precisam ser executadas. Nada é instanciado sozinho: a periodicidade só destaca
// os que já deveriam ter virado tarefa.
export function Templates() {
  const { data, isLoading, error } = useTemplates()
  const { organizacao } = useOrganizacaoAtual()
  const [params, setParams] = useFiltrosLembrados("templates")
  const texto = params.get("q") ?? ""
  const lista = (chave: string) => (params.get(chave) ?? "").split(",").filter(Boolean)
  const alterar = (chave: string, valor: string | null) => {
    const novos = new URLSearchParams(params)
    if (valor === null) novos.delete(chave)
    else novos.set(chave, valor)
    setParams(novos, { replace: true })
  }
  const todosOsTipos = tipos.map((t) => t.valor)
  const organizacoes = useMemo(() => [...new Set((data ?? []).map((t) => t.organizacao))].sort(), [data])
  const sel = params.has("tipo") ? lista("tipo") : todosOsTipos
  const orgs = params.has("org") ? lista("org") : organizacoes

  const filtrados = useMemo(() => {
    const termos = texto.toLowerCase().split(/\s+/).filter(Boolean)
    return (data ?? [])
      .filter((t) => sel.includes(tipoDe(t)))
      .filter((t) => !(params.has("org") && !organizacao) || orgs.includes(t.organizacao))
      .filter((t) => termos.every((termo) => `${t.id} ${t.titulo} ${t.descricao}`.toLowerCase().includes(termo)))
      .sort((a, b) => Number(b.devido) - Number(a.devido) || a.titulo.localeCompare(b.titulo))
  }, [data, params]) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <div className="space-y-4">
      <Filtros
        ativos={Number(params.has("tipo")) + Number(!organizacao && params.has("org"))}
        filtros={
          <>
        <SelecaoMultipla
          rotulo="Tipo"
          opcoes={tipos.map((t) => ({ ...t, contagem: (data ?? []).filter((x) => tipoDe(x) === t.valor).length }))}
          selecionados={sel}
          alterar={(v) => alterar("tipo", v.length === todosOsTipos.length ? null : v.join(","))}
          atalhos={[{ rotulo: "Todas", valores: todosOsTipos }]}
        />
        {!organizacao && (
        <SelecaoMultipla
          rotulo="Organização"
          opcoes={organizacoes.map((o) => ({ valor: o, rotulo: o }))}
          selecionados={orgs}
          alterar={(v) => alterar("org", v.length === organizacoes.length ? null : v.join(","))}
          atalhos={[{ rotulo: "Todas", valores: organizacoes }]}
        />
        )}
          </>
        }
      >
        <Input className="sm:w-56" placeholder="Buscar" value={texto} onChange={(e) => alterar("q", e.target.value || null)} />
        <NovoTemplate />
      </Filtros>
      <p className="text-muted-foreground text-xs">
        {filtrados.length} de {data?.length ?? 0} tarefas pré-moldadas ·{" "}
        <button type="button" className="underline" onClick={() => setParams(new URLSearchParams(), { replace: true })}>
          limpar filtros
        </button>
      </p>

      <Estado carregando={isLoading} erro={error} />
      {data && (
        <ul className="divide-y rounded-md border">
          {filtrados.map((t) => (
            <li key={t.id}>
              <Link to={`/templates/${t.id}`} className="block px-3 py-2.5 hover:bg-muted/50">
                <div className="flex items-start justify-between gap-2">
                  <span className="font-medium">{t.titulo}</span>
                  {t.devido && (
                    <Badge variant="outline" className="border-transparent bg-tom-atencao text-tom-atencao-fg">
                      Devida agora
                    </Badge>
                  )}
                </div>
                <div className="mt-1 flex flex-wrap gap-x-3 text-muted-foreground text-xs">
                  <span className="font-mono">{t.id}</span>
                  <span>{t.organizacao}</span>
                  <Periodicidade template={t} />
                  <span>
                    última instância {t.ultima_instancia_em ? <Data valor={t.ultima_instancia_em} /> : "nenhuma"}
                  </span>
                  {t.instancia_aberta && <span className="text-tom-andamento-fg">aberta: {t.instancia_aberta}</span>}
                </div>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function NovoTemplate() {
  const criar = useCriarTemplate()
  const navegar = useNavigate()
  const [aberto, setAberto] = useState(false)
  return (
    <Dialog open={aberto} onOpenChange={setAberto}>
      <DialogTrigger asChild>
        <Button variant="outline"><span className="sm:hidden">Nova</span><span className="hidden sm:inline">Nova pré-moldada</span></Button>
      </DialogTrigger>
      <DialogContent className="max-h-[90vh] overflow-y-auto">
        {aberto && (
          <FormularioDeTemplate
            titulo="Nova tarefa pré-moldada"
            fechar={() => setAberto(false)}
            pendente={criar.isPending}
            erro={criar.error}
            salvar={(d) =>
              criar.mutate(
                { ...d, periodicidade_dias: d.periodicidade_dias || undefined },
                { onSuccess: (t) => navegar(`/templates/${t.id}`) },
              )
            }
          />
        )}
      </DialogContent>
    </Dialog>
  )
}
