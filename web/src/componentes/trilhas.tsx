import { Fragment } from "react"
import { Link } from "react-router-dom"

import type { Requisito } from "@/api/cliente"

// Todas as trilhas do zero até o requisito, subindo pelos pais que estão no mapa (a organização atual). A derivação não tem ciclo; o limite
// só protege a tela de um grafo muito ramificado.
export function trilhasAteOZero(id: string, porId: Map<string, Requisito>, limite = 12): string[][] {
  const trilhas: string[][] = []
  const subir = (atual: string, caminho: string[]) => {
    if (trilhas.length >= limite) return
    const r = porId.get(atual)
    const pais = (r?.deriva_de ?? []).filter((p) => porId.has(p) && !caminho.includes(p))
    if (pais.length === 0) {
      trilhas.push([atual, ...caminho])
      return
    }
    for (const p of pais) subir(p, [atual, ...caminho])
  }
  subir(id, [])
  return trilhas
}

// Uma trilha como migalhas: Zero › Escritório › LGPD › …
export function Trilha({ ids, porId, incluirUltimo = true }: { ids: string[]; porId: Map<string, Requisito>; incluirUltimo?: boolean }) {
  const mostrados = incluirUltimo ? ids : ids.slice(0, -1)
  return (
    <span className="text-muted-foreground text-xs leading-relaxed">
      {mostrados.map((id, i) => (
        <Fragment key={`${id}-${i}`}>
          {i > 0 && <span className="mx-1">›</span>}
          <Link to={`/requisitos/${id}`} className="hover:text-foreground hover:underline">
            {porId.get(id)?.titulo ?? id}
          </Link>
        </Fragment>
      ))}
    </span>
  )
}

// Quantos requisitos há abaixo de um, contando cada um uma vez só.
export function descendentes(id: string, porId: Map<string, Requisito>): Set<string> {
  const vistos = new Set<string>()
  const descer = (atual: string) => {
    for (const f of porId.get(atual)?.derivados ?? []) {
      if (porId.has(f) && !vistos.has(f)) {
        vistos.add(f)
        descer(f)
      }
    }
  }
  descer(id)
  return vistos
}
