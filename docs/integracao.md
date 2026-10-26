# Integração

O rex não fala com nenhum sistema de notificação, celular ou assistente de voz. Ele publica o
que acontece e o que está pendente por interfaces genéricas, e cada instalação liga a elas o que
quiser por configuração, sem código particular neste repositório.

Três interfaces de saída, todas no contrato [`api/openapi.yaml`](../api/openapi.yaml):

| Interface | Para quem | Garantia |
|---|---|---|
| `GET /integracao/pendencias` | quem faz polling (um retrato do que pede atenção agora) | estado: quem perdeu uma chamada se recupera na seguinte |
| `GET /eventos?depois=<id>&tipo=…` | quem quer cada transição, sem perder nenhuma | cursor sobre o log de eventos, que nunca é apagado |
| ganchos (`-integracoes`) | quem quer ser avisado na hora, sem polling | pelo menos uma tentativa por evento, na ordem, sobrevivendo a reinícios; desistência registrada no log |

## Por que pendências + ganchos, e não só ganchos

O consumidor que motivou o desenho (uma integração pessoal que desenha na Dynamic Island e na
barra do i3, com polling de 1 min no laptop e uma função na nuvem) precisa de **estado**: um lembrete
disparado continua pedindo atenção até alguém confirmar ou adiar, e "silêncio nunca é tudo bem". Um
gancho perdido (consumidor fora do ar, rede caída) num desenho só de eventos deixaria o lembrete
sumir em silêncio. O retrato de pendências se corrige sozinho na chamada seguinte, e a lógica de
apresentação (quanto antes aparecer, quando tocar alarme) fica toda no consumidor: o rex
guarda só a hora marcada, e `horizonte` deixa o consumidor ver o que vence nas próximas horas.

Os ganchos existem para a latência (avisar no instante em que o lembrete é criado ou dispara, para
o consumidor reagendar o alarme no celular antes de o laptop desligar) e para consumidores que não
alcançam o rex por polling. Não são a fonte de verdade.

## Pendências

`GET /integracao/pendencias?horizonte=120` devolve:

- `lembretes`: os disparados (até confirmar ou adiar) e os agendados/adiados que vencem até
  `horizonte_ate`;
- `tarefas_em_validacao`: o que espera validação do humano;
- `sessoes_aguardando`: sessões cujo agente entregou ou está bloqueado e espera resposta
  (`POST /sessoes/{id}/espera`, desfeito por `DELETE` ou pelo encerramento);
- `ultimo_evento`: cursor para continuar por `GET /eventos?depois=`.

## Eventos

Todo evento tem `id` crescente, `momento`, `autor`, `entidade`, `tipo` e `dados`. Os que interessam
a integrações:

| Tipo | Entidade | Dados |
|---|---|---|
| `lembrete_criado` | L-… | `quando` |
| `lembrete_alterado` | L-… | campos alterados |
| `lembrete_disparado` | L-… | `quando`, `atrasado` |
| `lembrete_adiado` | L-… | `quando` (nova hora) |
| `lembrete_confirmado` | L-… | — |
| `lembrete_cancelado` | L-… | — |
| `tarefa_alterada` | T-… | campos alterados (`status: em_validacao` = foi para validação) |
| `agente_pedido` | T-… | `modelo`, `esforco`, `instrucao` |
| `sessao_iniciada`, `sessao_encerrada`, `sessao_substituida` | S-… | — |
| `sessao_aguardando` | S-… | `mensagem` |
| `sessao_retomada` | S-… | — |
| `integracao_falhou` | nome do gancho | `evento`, `erro` (nunca gera gancho) |

## Ganchos

`rex servir -integracoes integracoes.json`. Arquivo particular de cada instalação:

```json
{
  "fuso": "America/Sao_Paulo",
  "base_publica": "http://exemplo:8790",
  "ganchos": [
    {
      "nome": "barra",
      "eventos": ["lembrete_*", "sessao_aguardando", "sessao_retomada"],
      "comando": ["/caminho/do/script", "{tipo}", "{entidade}"]
    },
    {
      "nome": "nuvem",
      "eventos": ["lembrete_*"],
      "webhook": {
        "url": "https://exemplo/rex",
        "cabecalhos": { "Authorization": "env:TOKEN_DA_NUVEM" }
      }
    }
  ]
}
```

- `fuso`: em que fuso os lembretes são interpretados e a tela mostra as horas. Padrão: o da máquina.
- `base_publica`: base do campo `link` dos lembretes. Sem ela, `link` fica ausente.
- `turnos`: onde começam a manhã (e o dia), o almoço, a tarde e a noite, em `"HH:MM"`, para os
  lembretes de turno ou dia (ver abaixo). Padrão: `{"manha": "08:00", "almoco": "12:00", "tarde":
  "13:00", "noite": "18:00"}`; o turno omitido fica com o padrão.
- `eventos`: tipos exatos ou com `*` no fim.
- `comando`: argv, sem shell; aceita `{tipo}`, `{entidade}` e `{evento}` (id). Recebe no stdin o
  JSON da notificação. Saída diferente de 0 conta como falha.
- `webhook`: `POST` com o JSON da notificação no corpo; valores `env:NOME` nos cabeçalhos vêm do
  ambiente. Resposta diferente de 2xx conta como falha.

A notificação é `{"evento": Evento, "lembrete"|"tarefa"|"sessao": <estado atual da entidade>}`
(schema `Notificacao` no contrato), para o consumidor não precisar chamar a API de volta.

Cada gancho segue o log de eventos por um cursor gravado no banco: um evento por vez, na ordem do
log, fora das requisições (um gancho lento não atrasa a API). Depois de reiniciar, continua de onde
parou; um gancho novo começa no fim do log, sem receber o histórico. Cada falha é tentada de novo
3 vezes (1 s, 10 s, 60 s) e depois vira `integracao_falhou`, e a entrega segue para o próximo evento.
A notificação é montada na hora da entrega, com o estado mais recente da entidade.

### O lançador de agentes

O lançador (`-lancador`) é o primeiro caso deste padrão: reage a `tarefa_alterada` com status
entrando ou saindo de `em_andamento` rodando um comando configurado. Continua com a configuração
própria por enquanto; quando migrar, vira um gancho de um tipo especial (`"lancador": {…}`) na mesma
lista, sobre os mesmos eventos.

## Agendador de lembretes

Dentro do `rex servir`, a cada 15 s: todo lembrete agendado ou adiado com `quando` no
passado vira `disparado` e gera `lembrete_disparado`. Ao subir, o servidor dispara de uma vez os que
venceram enquanto ele estava parado, com `atrasado: true`; nada some em silêncio.

### Precisão: hora, turno, dia, semana ou mês

Todo lembrete tem `precisao`:

- `hora`: data e hora explícitas. É sensível ao horário: um consumidor pode avisar individualmente
  e tocar alarme antes da hora.
- `turno` (manhã, almoço, tarde ou noite), `dia`, `semana` (segunda a domingo) e `mes`: vale para a
  janela inteira, sem hora exata. O rex põe `quando` no início da janela (o turno que contém a hora
  pedida, e antes da manhã conta como manhã; dia, semana e mês começam na manhã do primeiro dia) e
  devolve `fim`. Por isso os lembretes do mesmo turno disparam juntos: o consumidor deve agrupá-los
  numa notificação em lote (mesmo `quando`, precisão diferente de `hora`), sem alarme sonoro, e pode
  mostrá-los até `fim`. Um lembrete de hoje cuja janela ainda não acabou é aceito e dispara na hora;
  disparar depois do início da janela nunca é `atrasado`. Adiar põe `quando` exatamente na nova hora,
  mantendo a precisão.

O `POST /lembretes` sem `precisao` assume `hora`; a ferramenta `criar_lembrete` do MCP exige que o
agente escolha, pelo que o usuário disse.

### Palavra-chave: `curto`

Opcional, até 10 caracteres: a palavra que identifica o compromisso ("ballet", "dentista"), para os
consumidores com pouco espaço (a ilha do iPhone mostra "16:50 ballet" num valor de 16 caracteres).
Sem ela, o consumidor usa o começo do texto. No `PATCH`, vazio remove.

## rex voz

O Rafael fala com as sessões de trabalho pelo modo de voz do app do Claude, com o rex como conector
personalizado (`/mcp`, ver [`login.md`](login.md)): o app chama as ferramentas `voz_*`, e o rex fala
com cada agente pelo tmux dele, como o Rafael faria no laptop. Rotas (contrato em
`api/openapi.yaml`):

- `GET /voz/sessoes` (`voz_panorama`): as sessões ativas, com estado, o que
  pediram ao Rafael, quantas novidades ele não ouviu e o último texto do agente.
- `POST /voz/sessoes/{id}/mensagens` digita a frase no agente como `[rex voz · pedido N] …`. Com o
  agente trabalhando, o Claude Code a põe na fila dele, em ordem. Recusada (409) numa tela de
  permissão. `{id}` é a sessão ou uma tarefa com sessão ativa.
- `GET /voz/sessoes/{id}` diz o estado do agente, lido da tela: `trabalhando` ("esc to interrupt"
  na barra), `pedindo_permissao` (opções numeradas; vem a `tela`), `ocioso`, `sem_agente` (tmux não
  achado) ou `encerrada`. Traz também as novidades do transcript do agente
  (`~/.claude/projects/*/<chat>.jsonl`) depois do ponteiro: falas do Rafael, mensagens de outras
  sessões (`de: sessao`, com `origem`) e textos do agente, inclusive a narração do meio do turno,
  que o Claude Code grava como bloco `thinking` com texto (o raciocínio fica gravado vazio).
  `esperar` (até 20 s) segura a resposta enquanto o agente trabalha calado.
- Ponteiro: cada sessão guarda o uuid da última linha do transcript que o Rafael ouviu
  (`voz_lido_ate`). Só `GET /voz/sessoes/{id}?ouvir=true` o avança: é o que o conector usa, porque o
  app fala ao Rafael o que recebe. As `voz_*` do MCP local (de um agente) só leem.
- `POST /voz/sessoes/{id}/teclas` aperta 1–4 ou Enter numa tela de permissão (a escolha que o Rafael
  falou) ou Escape para interromper.

### Entre sessões

`POST /sessoes/{id}/mensagens` (`enviar_a_sessao`, só no MCP dos agentes) é a mesma entrega pelo tmux,
mas de uma sessão a outra: a origem é a dona do token. A mensagem chega como
`[rex · de S-0001 nome] … — (mensagem da sessão …: responda com a ferramenta enviar_a_sessao do rex para
S-0001; …)`, e a resposta, pela mesma rota no sentido inverso, entra no chat da origem como mensagem
nova. Assim nenhuma sessão precisa ler a conversa ou a tela da outra para saber o que ela respondeu.
Cada mensagem vira o evento `mensagem_de_sessao` na sessão de destino.
