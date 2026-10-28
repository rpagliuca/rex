---
name: rex-coordenacao
description: "Coordenar o trabalho de agentes de IA pelo rex: criar tarefas, pôr um agente para trabalhar numa tarefa, pausar, bloquear, mandar para validação, concluir ou retomar tarefas de outros agentes. Use quando o usuário pedir para criar uma tarefa ou um agente, iniciar, pausar, bloquear, retomar ou concluir uma tarefa que não é a deste chat, ou revisar o que está em andamento. Triggers: criar tarefa, criar agente, novo agente para, subir um agente, pausar a tarefa, bloquear a tarefa, retomar a tarefa, concluir a tarefa, mover para validação, o que está em andamento, matar o agente de, encerrar o agente de."
user-invocable: true
---

# rex: coordenar agentes

O rex é a fonte única do status das tarefas. Coordenar é **mudar o status**; o resto é
consequência:

| Você muda | O rex faz |
|---|---|
| tarefa nova ou existente → `em_andamento`, sem sessão ativa | o lançador sobe um agente para ela, numa sessão tmux |
| `em_andamento` → qualquer outro status | o lançador pede ao agente vivo que encerre a sessão com resumo; o agente fecha o próprio tmux |
| entre outros status (`em_validacao` → `concluida`...) | nada além do status |

Você não cria sessões tmux, não escolhe pastas e não fecha agentes à mão.

Se as ferramentas `mcp__rex__*` não estiverem disponíveis, diga isso ao usuário e pare.

## Status

`a_fazer` (registrada, sem agente) · `em_andamento` (com agente trabalhando) · `pausada` (o usuário
decidiu parar) · `bloqueada` (dependência externa) · `em_validacao` (pronta, falta o usuário validar)
· `concluida`.

`pausada` e `bloqueada` param do mesmo jeito; muda a causa. Para `bloqueada`, diga ao usuário o que
bloqueia e o que precisa acontecer para destravar, e registre isso na descrição da tarefa
(`alterar_tarefa`, acrescentando uma seção "Bloqueio").

## Criar uma tarefa

`criar_tarefa` com:

- `titulo`: o trabalho, curto e específico;
- `organizacao`: a do pedido (pergunte se não estiver claro);
- `descricao`: objetivo, contexto e restrições que o agente precisa para começar sem perguntar. Não
  pesquise antes: o agente da tarefa faz a pesquisa dele;
- `requisitos` e `componentes`: o porquê e onde (`buscar_requisitos`, `listar_componentes`);
- `status`: `a_fazer`. Para pôr outro agente nela, `lancar_agente` (abaixo).

Uma tarefa que **este chat** cria ou põe em andamento é entendida como trabalho deste chat: não sobe
agente. Para delegar, use sempre `lancar_agente`.

Para uma tarefa recorrente, procure antes uma pré-moldada: `listar_templates` e
`instanciar_template(template, contexto)`; o contexto vai no topo da descrição e o agente dá o título.

## Gerenciar agentes

Cada agente custa tokens e roda sozinho em tmux: suba um só quando o usuário pediu (ou aprovou) e
diga qual, para quê e com qual modelo e esforço.

- **Subir**: `lancar_agente(tarefa, instrucao?, modelo?, esforco?, nome?)`. Sem modelo e esforço, vale o
  padrão do lançador (`opcoes_do_lancador`). A `instrucao` é só o que a tarefa não diz (ex.: o link do
  PR); o agente lê a tarefa e as sessões anteriores. Recusa tarefa com sessão ativa.
- **Memória**: o `lancar_agente` mede a máquina antes de subir e recusa com pouca RAM disponível,
  devolvendo o que ocupa e as sessões que esperam o Rafael há mais tempo. Passe isso a ele e sugira o
  que encerrar; só repita com `forcar: true` se ele mandar abrir assim mesmo. Para olhar antes,
  `recursos_da_maquina` (RAM, sessões, itens abertos, docker e os maiores processos por RAM e por
  CPU). Para liberar, `descer_item` fecha um item aberto (Steam, ambiente docker, agente navegador…),
  só com o sim do Rafael para aquele item; uma recusa (lock com dono) é a resposta: diga-a a ele.
- **Nome**: se você sabe do que o agente vai tratar, passe `nome`: o assunto em poucas palavras
  (`dados-privado-joao`, `lembretes-time-sensitive`). É o que o Rafael vê no tmux e na lista do Claude
  Code web. Sem verbo genérico ("retomar", "revisar") e sem apelido de repositório. Sem `nome`, ele
  sai do título, e o agente o corrige depois se ficar ruim.
- **Conferir**: em cerca de um minuto, `obter_tarefa` deve mostrar `sessao_ativa`. Sem ela, o agente
  não subiu: avise o usuário.
- **Quem está trabalhando**: `agentes_ativos` (sessão, nome, agente, tarefas).
- **Parar**: mude o status da tarefa (`alterar_tarefa`: `pausada`, `bloqueada`, `em_validacao`,
  `concluida`). O agente encerra a sessão com resumo e fecha o próprio tmux. Nunca mande mensagem
  ao tmux de outro agente nem o feche.
- **Retomar**: `lancar_agente` de novo; o agente novo começa pelo resumo da última sessão.
- **Conversar com um agente vivo** (perguntar, combinar algo): `enviar_a_sessao(sessao, texto)`. A
  mensagem chega identificada por você e com a instrução de responder pela mesma ferramenta; a
  resposta entra no seu chat como mensagem nova. Não use `voz_enviar` (é a voz do Rafael), não fique
  consultando `voz_situacao` e nunca leia a tela do tmux dele para achar a resposta.

## Pausar, bloquear, validar, concluir ou retomar

- **Parar** (`pausada`, `bloqueada`, `em_validacao`, `concluida`): `alterar_tarefa(id, status)`. Se
  havia agente, ele encerra com resumo sozinho. Não mande mensagens a ele nem feche o tmux dele.
- **Retomar**: `lancar_agente(id)`. Sobe um agente novo, que começa pelo resumo da última sessão.
- **Tarefa com sessão ativa mas sem agente vivo**: encerre pelo frontend (encerramento manual, com
  resumo). Não use a API por fora.

## Achar uma tarefa

O MCP não lista todas as tarefas, de propósito: custa muitos tokens e quase nunca é preciso. Use o id
que o usuário citar. Para "o que está em validação?" ou "o que tenho da Yes a fazer?",
`consultar_tarefas` com ao menos um filtro (status, organização, componente ou requisito): só id,
título, status, organização e sessão ativa, até 20 (`total` e `fora` dizem quantas casaram e quantas
não vieram); com muitas fora, refine o filtro em vez de subir o limite. Com o id, `obter_tarefa` e
`historico_da_tarefa` dão o estado e as sessões.

## Requisitos e a cadeia

Um **Requisito** (`R-0001`) nunca fecha: é o que precisa ser (e continuar) verdade. Tarefas tentam
atendê-lo. Cada requisito tem uma **Origem** (`O-0001`): uma pessoa que pediu ou um documento (lei,
norma, contrato). Requisitos derivam de outros, formando um grafo: "backup em dois destinos" deriva
de "cópia fora do ambiente principal", que deriva de "cumprir a lei de dados".

- **Um só requisito zero**: o primeiro criado, o único sem pais. Todo outro requisito deriva de
  algum (sem `deriva_de`, deriva do zero), então todo requisito tem uma trilha até o zero: é ela que
  dá o contexto inteiro dele.
- **Requisito é uma afirmação testável (falseável)**, no presente, sobre o estado do mundo — não uma
  ação. "O backup do cartório tem cópia em dois destinos independentes, verificada toda semana" ✅;
  "Fazer backup" ❌ (isso é tarefa). Quanto mais abaixo na cadeia, mais concreto e verificável.
- **Boa prática: no máximo 5 requisitos derivados de cada um.** Passou disso, agrupe-os sob
  requisitos intermediários (cada um também uma afirmação testável). Não é bloqueado; o requisito
  só passa a pedir atenção.
- O `estado` é calculado e limitado pelo menos maduro abaixo (`limitado_por` diz quem). Do menos
  ao mais maduro: `nao_atendido` (tarefa aberta fora de andamento, ou nem tarefa nem execução),
  `em_trabalho`, `nao_validado` (tarefas todas concluídas, sem execução), `validacao_expirada`,
  `validado`. Sem tarefa e sem execução, um requisito com derivados é decidido só por eles. `estado_proprio` ignora os de
  baixo. Tarefas concluídas **não** provam nada.
- **Triagem por pontos de importância:** os pontos ficam na ligação com cada pai (`definir_pontos`) e
  só se comparam entre irmãos: a fatia é pontos / soma dos irmãos; o `peso` no total soma, sobre os
  pais, peso do pai × fatia (o zero vale 100%). Sem pontos, a ligação não foi triada (conta como 1).
  A triagem é breadth-first, a partir do zero; `arvore_de_requisitos` mostra pontos, fatia e peso.
  Os pontos são escolhidos pelo Rafael: ao propor, diga o porquê; só altere com o aval dele. A
  antiga `prioridade` (alta, media, baixa) é só histórico.
- `registrar_execucao` registra uma verificação de conformidade, com a evidência e a validade em
  dias. Só registre o que foi verificado de fato, agora; nunca porque as tarefas acabaram.
- O campo `atencao` de cada requisito diz o que falta para ele estar bem cuidado (muitos filhos,
  sem origem, sem descrição, sem tarefa, trabalho parado). Ao mexer num requisito, resolva o que
  estiver ao alcance.
- Antes de criar uma origem, `listar_origens`: reaproveite a existente.
- `criar_requisito` com `origem` e `deriva_de`; sem `organizacao`, herda a do primeiro pai.
- `derivar_requisito` acrescenta um pai (um requisito pode derivar de vários); ciclos são recusados.
- `vincular_requisito` liga uma tarefa ao requisito que ela tenta atender.
- Registre o pedido verbatim na descrição do requisito: é a evidência de onde ele veio.

## Responder ao usuário

Diga o id da tarefa, o status novo e o que vai acontecer ("o agente vai encerrar com resumo", "um
agente está subindo").
