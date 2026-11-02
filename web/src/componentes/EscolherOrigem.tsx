import { useState } from "react"

import { type Origem, useCriarOrigem, useOrigens } from "@/api/cliente"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"

const NENHUMA = "nenhuma"
const NOVA = "nova"

// Formulário para criar uma origem (pessoa ou documento).
export function CriarOrigem({ criada, cancelar }: { criada: (id: string) => void; cancelar: () => void }) {
  const criar = useCriarOrigem()
  const [nome, setNome] = useState("")
  const [tipo, setTipo] = useState<Origem["tipo"]>("pessoa")

  return (
    <div className="flex flex-wrap items-center gap-2">
      <Input className="sm:w-60" placeholder="Nome (ex.: Lei 13.709/2018 (LGPD))" value={nome} onChange={(e) => setNome(e.target.value)} />
      <Select value={tipo} onValueChange={(v) => setTipo(v as Origem["tipo"])}>
        <SelectTrigger className="w-36">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="pessoa">Pessoa</SelectItem>
          <SelectItem value="documento">Documento</SelectItem>
        </SelectContent>
      </Select>
      <Button
        type="button"
        size="sm"
        disabled={!nome.trim() || criar.isPending}
        onClick={() =>
          criar.mutate(
            { nome: nome.trim(), tipo },
            {
              onSuccess: (o) => {
                setNome("")
                criada(o.id)
              },
            },
          )
        }
      >
        Criar origem
      </Button>
      <Button type="button" size="sm" variant="ghost" onClick={cancelar}>
        Cancelar
      </Button>
      {criar.error && <p className="w-full text-destructive text-sm">{criar.error.message}</p>}
    </div>
  )
}

// Escolhe uma origem existente ou cria uma na hora (pessoa ou documento).
export function EscolherOrigem({ valor, alterar }: { valor: string; alterar: (id: string) => void }) {
  const origens = useOrigens()
  const [nova, setNova] = useState(false)

  if (nova) {
    return (
      <CriarOrigem
        criada={(id) => {
          alterar(id)
          setNova(false)
        }}
        cancelar={() => setNova(false)}
      />
    )
  }
  return (
    <Select
      value={valor || NENHUMA}
      onValueChange={(v) => (v === NOVA ? setNova(true) : alterar(v === NENHUMA ? "" : v))}
    >
      <SelectTrigger className="sm:w-72">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value={NENHUMA}>Sem origem</SelectItem>
        {(origens.data ?? []).map((o) => (
          <SelectItem key={o.id} value={o.id}>
            {o.nome} <span className="text-muted-foreground">({o.tipo})</span>
          </SelectItem>
        ))}
        <SelectItem value={NOVA}>+ Nova origem…</SelectItem>
      </SelectContent>
    </Select>
  )
}
