---
name: rex
description: "Ciclo de trabalho de um agente de IA no rex pelas ferramentas MCP: toda sessão de trabalho é registrada antes da primeira ação que muda estado; abrir ou adotar a Sessão de Trabalho, achar ou criar a Tarefa, escrever o journal, mudar o status, entregar e esperar (nunca se encerrar sem pedido claro do Rafael ou do lançador), encerrar com resumo. Use SEMPRE no início de qualquer trabalho num chat em que as ferramentas mcp__rex__* estejam disponíveis — investigar, alterar, escrever código, documentar, operar sistemas —, qualquer que seja a origem do chat. Use também ao retomar ou encerrar trabalho, quando o prompt citar uma tarefa T-0001 ou sessão S-0001, quando REX_TAREFA ou REX_SESSAO estiver no ambiente, ou quando pedirem para este chat virar (se promover a) uma tarefa. Triggers: começar a trabalhar, continue a partir do CLAUDE.md, retomar, rex, tarefa T-, sessão S-, iniciar_sessao, sessao_atual, encerrar_sessao, repassar_sessao, resumo para retomar, journal da sessão, a tarefa foi movida, a tarefa já tem a sessão ativa, se promover a tarefa, criar lembrete, me lembra de, alterar lembrete."
user-invocable: true
---

# rex: o ciclo de um agente

O rex é a fonte única de **Tarefas** (o que fazer, com status) e **Sessões de Trabalho**
(quem trabalhou, quando, com qual agente e onde está o journal). Você só escreve nele pelas
ferramentas MCP `mcp__rex__*`. O servidor impõe as regras; uma recusa é a resposta certa, não
um obstáculo a contornar.

Se as ferramentas não estiverem disponíveis, diga isso ao usuário e pare. Não fale com a API por
`curl` nem edite o banco.

## Conceitos em uma linha

- **Tarefa** (`T-0001`): status `a_fazer`, `em_andamento`, `pausada`, `bloqueada`, `em_validacao` ou
  `concluida`, e um **resumo para retomar**, escrito no encerramento da última sessão.
- **Sessão de Trabalho** (`S-0001`): este chat, com um **agente imutável**. Uma tarefa tem no máximo
  uma sessão ativa; várias sessões, em máquinas diferentes, trabalham nela ao longo do tempo.
- **Journal**: o seu registro do trabalho, num arquivo da **pasta da sessão**, criada pelo
  `iniciar_sessao`. Artefatos da sessão ficam na mesma pasta.
- **Lançador**: quando alguém põe uma tarefa em andamento, ele sobe um agente para ela (talvez você)
  com `REX_TAREFA` no ambiente; quando alguém a tira de andamento, ele pede ao agente que
  encerre.

## Ao começar

**Toda sessão de trabalho é registrada, antes da primeira ação que muda estado**, qualquer que seja
a origem do chat. Só fica de fora a pergunta rápida que não muda nada; na dúvida, registre.
Primeiro, `sessao_atual`: se o lançador já abriu a sua sessão, siga o primeiro caminho abaixo.

Há dois caminhos.

**Você foi lançado para uma tarefa** (o prompt cita a tarefa; `REX_SESSAO` está no ambiente):

1. A sua sessão de trabalho **já está aberta**: o lançador a abriu com o seu modelo e esforço.
   `sessao_atual` mostra qual é e o `journal` (`<máquina>:<caminho>/S-0001/journal.md`), já criado.
   Escreva nele. (`iniciar_sessao`, se chamado, devolve essa mesma sessão.)
2. `obter_tarefa(id)`: leia o **resumo** primeiro; é o que a sessão anterior deixou para você.
3. `historico_da_tarefa(id)`: as sessões anteriores, com o journal de cada uma. Leia os journals que
   o resumo não cobrir.
4. Se o prompt trouxer uma **instrução adicional de reabertura**, ela é o ponto de partida; o resto
   do contexto vem da tarefa e das sessões anteriores.
5. Confira o **nome** da sessão (`sessao_atual`): é o que o Rafael vê no tmux e na lista do Claude
   Code web. Se não disser o assunto do trabalho, troque-o com `renomear_sessao` (ver "Durante").

Nada que mude estado (comandos, PRs, consultas que alterem algo) antes de conferir a sessão.

**O usuário pediu para este chat virar uma tarefa** (ou começou a trabalhar numa sem o lançador):

1. `iniciar_sessao` com `modelo` e sem tarefas.
2. Se a tarefa já existe, o usuário diz o id; senão, `criar_tarefa` (título, organização, descrição,
   `status: "em_andamento"`, e já os `requisitos` e `componentes`; ver "Ligar ao porquê" abaixo).
   Uma tarefa criada por uma sessão não aciona o lançador.
3. `vincular_tarefa` com o id. Avise o usuário em uma linha qual tarefa foi criada ou vinculada.

Uma sessão por chat. Para mais uma tarefa no mesmo trabalho, `vincular_tarefa`, não outra sessão.

Se a tarefa tem `requisitos`, leia-os com `obter_requisito`: são o **porquê** do trabalho, e as
`trilhas` mostram a cadeia até o zero em uma linha cada (de onde vem cada exigência).

## Ligar ao porquê (barato)

Toda tarefa tenta atender algum requisito e mexe em algum componente. Para achá-los gastando pouco:

1. `buscar_requisitos` com 2–4 palavras do assunto (e `organizacao`): uma chamada, só id e título.
   Tente outras palavras antes de concluir que não existe.
2. `obter_requisito` no candidato, se precisar confirmar: as `trilhas` dão o contexto.
3. `listar_componentes` para reaproveitar um componente existente.
4. Só para criar um requisito novo, `arvore_de_requisitos` (da organização, ou do componente) para
   escolher o pai. Requisito é afirmação testável; no máximo 5 derivados por pai.

Na dúvida, não ligue: tarefa sem requisito aparece para o Rafael na triagem. Ligar errado é pior.

## Durante

- Escreva o journal: o que fez, o que decidiu e por quê, o que falhou, onde estão os artefatos.
- Ao criar artefatos fora da pasta da sessão, registre-os com `atualizar_journal`.
- **Nome**: a cada etapa significativa, confira se o nome da sessão ainda diz em poucas palavras do
  que você trata (`dados-privado-joao`, não `retomar-implantacao`). Se o trabalho mudou de assunto ou
  o nome ficou ruim, `renomear_sessao(nome)`: o lançador renomeia o tmux e manda `/rename` ao Claude
  Code (vale também no remote-control), sem interromper o turno. Sem verbo genérico e sem apelido de
  repositório.
- Toda alteração que você fizer pelas ferramentas fica atribuída à sua sessão no log de eventos.

## Falar com outra sessão

- **Mensagem que chega** como `[rex · de S-0254 nome] …` vem de outra sessão, não do Rafael. Responda
  com `enviar_a_sessao(sessao: "S-0254", texto)`: a resposta entra no chat dela. Responder só no seu
  chat não chega a ela.
- **Para perguntar** a outra sessão, `enviar_a_sessao` também. A resposta chega aqui como mensagem
  nova: não fique consultando `voz_situacao` e **nunca leia a tela do tmux de outro agente**
  (`capture-pane`) para achar a resposta. `voz_enviar` é a voz do Rafael; não use para falar em seu
  nome.

## Ao terminar o trabalho: entregue e espere

**Você não encerra a sessão por conta própria.** Terminar a entrega, ficar bloqueado, precisar de
uma aprovação ou de uma resposta do Rafael **não** são motivos para encerrar: ele acompanha pelo
remote-control e pode querer continuar, corrigir ou aprovar algo. Um agente parado esperando não
custa nada; um agente que se encerrou sozinho obriga a subir outro e reler tudo.

1. Escreva no journal e persista-o (commit só da pasta da sessão e push; se o push for recusado,
   `git pull --rebase` e push de novo; commits sem assinatura, se a máquina não tiver como assinar).
2. Mande ao Rafael a entrega ou o bloqueio, curto: o que ficou feito, o que falta e o que precisa
   dele (perguntas numeradas, alternativas em letras). Se uma ferramenta pediu aprovação e não havia
   quem respondesse, diga exatamente o que ele precisa fazer.
3. `aguardar_resposta` com o que você precisa dele, em uma linha ("aprovar o PR #51", "decidir 1A ou
   1B"): a espera aparece nas integrações da instalação (celular, barra) até você seguir.
4. **Espere.** A tarefa fica em `em_andamento` e a sessão aberta. Quando ele responder e você voltar
   a trabalhar, `retomar_trabalho`.

Encerre **só** quando o desejo de encerrar for claro:

- **O Rafael pediu**, com palavras que não deixam dúvida ("pode encerrar", "fecha a sessão", "terminou,
  pode concluir"). "Ok", "valeu" ou silêncio não são pedido de encerramento. Na dúvida, pergunte.
- **O lançador pediu** (a tarefa foi movida por outra pessoa): ver a seção seguinte.

Para encerrar, a pedido:

1. Ajuste o status, se o Rafael não disse outro: `em_validacao` (pronto, falta ele validar),
   `concluida`, `bloqueada` (dependência externa; diga qual no resumo) ou `pausada`.
2. Persista o journal (como acima).
3. `encerrar_sessao` com o **resumo**. Ele vai para cada tarefa da sessão e é a primeira coisa que o
   próximo agente lê. Escreva para quem não viu nada desta conversa:
   - em que estado a tarefa ficou;
   - o que falta, começando pelo próximo passo concreto;
   - decisões e descobertas que o próximo não pode perder;
   - onde está o detalhe (journal, PR, arquivo).
4. Mensagem final curta e, **como última ação**, se você roda numa sessão tmux (`$TMUX` preenchido),
   feche-a: `tmux kill-session -t "$(tmux display-message -p '#S')"`. Nada depois disso. Fora do
   tmux (um chat comum), não feche nada.

## Quando o lançador pede para encerrar

A mensagem diz que a tarefa foi movida para outro status por outra pessoa. **Não altere o status**:
quem moveu já decidiu. Pare o que estiver fazendo, registre no journal onde parou, chame
`encerrar_sessao` com o resumo para retomar, persista o journal e feche o seu tmux como nos passos 2 a
4 acima. Esse é o único caso em que você encerra sem o Rafael pedir no chat.

## Recusas e o que fazer

| Mensagem | Significa | Faça |
|---|---|---|
| "a tarefa T-… já tem a sessão ativa S-…" | Outro chat está trabalhando nela | Pare e avise o usuário. Nunca encerre a sessão de outro chat |
| "este chat já tem a sessão S-… aberta" | Você já iniciou | Use `vincular_tarefa` ou continue na mesma |
| "não há sessão aberta neste chat" | Faltou `iniciar_sessao` | Inicie antes de operar a sessão |
| "o resumo é obrigatório" | Encerramento sem resumo | Escreva o resumo |

## Troca de agente

Se o usuário trocar o modelo ou o esforço no meio do chat, você deixou de ser o agente registrado.
Chame `repassar_sessao` com o `modelo` e o `esforco` novos (aqui o esforço é obrigatório): a sessão
atual termina como substituída e outra começa com as mesmas tarefas. Só quando a troca aconteceu.

## Lembretes

Compromisso que o Rafael pediu para lembrar (ou que a tarefa exige, como "renovar o certificado na
sexta às 9"): `criar_lembrete` com `quando` em RFC 3339 com fuso, ligado à tarefa quando for dela. A
`precisao` sai do que ele disse: `hora` só com data e hora explícitas ("sexta às 9"), `turno` para
"de manhã", "no almoço", "à tarde" ou "à noite", `dia` para "amanhã" ou "sexta" sem hora, `semana`
para "semana que vem" ou "próxima semana", `mes` para "em abril" ou "mês que vem"; com o dia dentro
deles ("quarta da semana que vem"), `dia`. Nunca invente uma hora para o que não tem: só `hora` toca
alarme; os demais são avisados em lote, sem som. Preencha `curto` com a palavra-chave do compromisso
(até 10 caracteres: "ballet", "dentista", "Vivo"): é o que cabe no resumo da ilha do iPhone, e sem
ela a ilha mostra o começo do texto. `proximos_lembretes` mostra o que vence, com a `situacao` de
cada um: só `vencido` é atrasado; o de semana ou mês dispara na segunda ou no dia 1 e fica `na_janela`
até o fim dela, então nunca entra num "adia os de ontem";
`confirmar_lembrete` só quando ele disser que viu ou fez. Para mudar o texto, o curto, a hora, a precisão ou a
ligação de um lembrete, `alterar_lembrete` (mantém o id); `cancelar_lembrete` só quando ele disser
que o lembrete não vale mais. Nunca cancele e recrie para editar: o Rafael vê o antigo como
cancelado, e cancelado não volta. O rex guarda a hora; avisar com antecedência é das integrações.

## O que não fazer

- Não altere o resumo com `alterar_tarefa` para registrar progresso: o resumo é do encerramento.
- Não abra sessão só para ler; ler não exige sessão.
- Não mantenha status de tarefa em outro lugar (pastas, listas, arquivos): o status é o do rex.
- Não mostre nem procure o token da sessão: ele fica no processo MCP.
- Nunca feche o tmux de outro agente; só o seu, e só depois de encerrar a sua sessão.
- Para criar tarefas para outros agentes, pausar ou mover tarefas que não são suas: skill
  `rex-coordenacao`.
