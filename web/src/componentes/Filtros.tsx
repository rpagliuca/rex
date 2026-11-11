import { useState, type ReactNode } from "react"
import { ChevronDown, SlidersHorizontal } from "lucide-react"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

// Barra de filtros das listas. No celular os filtros vêm colapsados atrás de um botão e só a busca e a
// ação ficam à vista; a partir de sm tudo fica numa linha só, como sempre foi.
export function Filtros({ ativos, filtros, children }: { ativos: number; filtros: ReactNode; children: ReactNode }) {
  const [aberto, setAberto] = useState(false)
  return (
    <div className="flex flex-col gap-2 sm:flex-row sm:flex-wrap">
      <div className={cn("order-2 flex-col gap-2 sm:contents", aberto ? "flex" : "hidden")}>{filtros}</div>
      <div className="order-1 flex gap-2 sm:contents">
        <Button variant="outline" className="shrink-0 sm:hidden" aria-expanded={aberto} onClick={() => setAberto(!aberto)}>
          <SlidersHorizontal />
          Filtros
          {ativos > 0 && <span className="rounded-full bg-muted px-1.5 text-xs font-medium">{ativos}</span>}
          <ChevronDown className={cn("transition-transform", aberto && "rotate-180")} />
        </Button>
        {children}
      </div>
    </div>
  )
}
