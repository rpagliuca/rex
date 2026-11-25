# Identidade visual do rex

A interface é **tinta sobre papel**: preto e branco levemente quentes, sem cor de marca. **Só o
estado tem cor**, então o que pede atenção (uma tarefa bloqueada, um requisito não atendido) se
destaca sem disputar com botões e menus.

## Regras

1. **Nada de cor literal nos componentes.** Nada de `bg-blue-100`, `text-amber-600` ou hex no TSX.
   Cor vem de um token: os neutros do shadcn (`background`, `foreground`, `muted`, `border`,
   `primary`...) ou um dos tons semânticos abaixo.
2. **Cor significa estado.** Um tom só aparece onde há um estado a comunicar. Elementos de
   interface (botões, abas, links) ficam nos neutros; o botão principal é tinta (`primary`).
3. **Links são sublinhados**, não coloridos. A exceção é a referência a uma sessão ativa
   (`● S-0001`), que leva o tom *andamento* porque é um estado.
4. **Os dois temas são de primeira classe.** O tema segue o sistema (`prefers-color-scheme`).
   Todo token tem valor no claro (`:root`) e no escuro (`.dark`) em `web/src/index.css`.
5. **Celular primeiro.** Alvos de toque confortáveis, nada que exija passar o mouse por cima para
   ser lido, e listas que quebram linha em vez de rolar para o lado.

## Tokens

Definidos em `web/src/index.css`, em `oklch`. Neutros com matiz 80 (leve calor) e croma baixo.

| Token | Uso |
|---|---|
| `background` / `foreground` | papel e tinta |
| `card`, `popover` | superfícies acima do papel |
| `muted` / `muted-foreground` | fundos discretos (aba ativa) e texto secundário |
| `border`, `input`, `ring` | contornos e foco |
| `primary` | ação principal: tinta no claro, papel no escuro |
| `destructive` | ação que apaga ou encerra |

### Tons semânticos

Cada tom tem três tokens: `--tom-X` (fundo suave), `--tom-X-fg` (texto) e `--tom-X-borda`. No
Tailwind: `bg-tom-X`, `text-tom-X-fg`, `border-tom-X-borda`. As combinações prontas ficam em
`web/src/componentes/tons.ts` (`seloDoTom`, `contornoDoTom`, `textoDoTom`); use-as em vez de
montar as classes à mão.

| Tom | Matiz | Status de tarefa | Estado de requisito | Prioridade | Outros |
|---|---|---|---|---|---|
| `neutro` | cinza-azulado | a fazer | não validado | baixa | |
| `andamento` | azul | em andamento | em trabalho | | sessão ativa |
| `atencao` | âmbar | pausada | validação expirada | média | pré-moldada devida, pedidos de atenção |
| `perigo` | vermelho | bloqueada | não atendido | alta | |
| `validacao` | violeta | em validação | | | |
| `sucesso` | verde | concluída | validado | | |

O mapeamento de estado para tom vive em `web/src/componentes/rotulos.tsx`. Um estado novo escolhe
um tom existente; um tom novo só entra se nenhum dos seis servir, e com matiz distante dos outros.

## Forma

- **Fonte:** a do sistema (`system-ui`). Identificadores (`T-0001`, `S-0001`) podem usar a monoespaçada
  do sistema quando alinhados em coluna.
- **Cantos:** `--radius` de 10px; selos (badges) em pílula.
- **Densidade:** confortável; entre listas e seções, espaço com `gap`, não com margens soltas.

## Logotipo

**Tiranossauro:** um rex em traço, de perfil, segurando com o braço curto uma tarefa feita (um
quadrado com check). O nome ao lado, em minúsculas e em negrito: **rex**, inclusive no início de frase.

![Logotipo do rex](logotipo.svg)

- Desenho vetorizado da arte escolhida (T-0216), em uma cor só: preenchido com
  `currentColor`, segue a tinta nos dois temas.
- Componentes em `web/src/componentes/Logotipo.tsx`: `Assinatura` (símbolo e nome, usado no topo)
  e `Logotipo` (só o símbolo). Favicon: `web/public/favicon.svg`, só a tarefa feita que o rex segura (a caixa inclinada com o check); o desenho inteiro não cabe em 16px. Arquivo para fora
  do app: `docs/logotipo.svg`.
- O nome faz parte do desenho; não recompor a assinatura com texto em outra fonte.
- Não colorir com um tom semântico, não preencher o contorno do rex, não usar a assinatura abaixo
  de 20px de altura.
