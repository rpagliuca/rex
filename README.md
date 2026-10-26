# rex

Coordenação de humanos e agentes de IA orientada por requisitos vivos, com evidência contínua.

Desenho completo em [`docs/arquitetura.md`](docs/arquitetura.md); identidade visual em [`docs/design.md`](docs/design.md).

## Estado atual

Prova de conceito de **Tarefas** e **Sessões de Trabalho**: API HTTP sobre SQLite, com o contrato em
[`api/openapi.yaml`](api/openapi.yaml) e as invariantes impostas pela API e pelo banco, mais um
servidor MCP para agentes de IA.

## Rodar

```bash
go build -o rex ./cmd/rex
./rex servir -banco rex.db -endereco 127.0.0.1:8787
```

## Usar com um agente (MCP)

O servidor MCP fala com a API e guarda, em memória, o token da sessão aberta no chat; o agente nunca
o vê. No Claude Code:

```bash
claude mcp add rex -- /caminho/para/rex mcp -api http://127.0.0.1:8787
```

Para os agentes saberem usar as ferramentas, instale as skills de [`skills/`](skills):
[`rex`](skills/rex/SKILL.md) (o agente trabalhando numa tarefa) e
[`rex-coordenacao`](skills/rex-coordenacao/SKILL.md) (criar tarefas, subir, pausar e
retomar agentes):

```bash
for skill in skills/*/; do ln -sfn "$PWD/$skill" ~/.claude/skills/"$(basename "$skill")"; done
```

Ferramentas: `obter_tarefa`, `consultar_tarefas`, `criar_tarefa`, `alterar_tarefa`,
`historico_da_tarefa`, `iniciar_sessao`, `sessao_atual`, `vincular_tarefa`, `atualizar_journal`, `renomear_sessao`,
`encerrar_sessao`, `repassar_sessao`, `listar_eventos`, `buscar_requisitos`, `arvore_de_requisitos`, `obter_requisito`, `criar_requisito`,
`alterar_requisito`, `derivar_requisito`, `marcar_obsoleto`, `vincular_requisito`, `registrar_execucao`, `listar_componentes`, `criar_componente`, `vincular_componente`, `opcoes_do_lancador`, `lancar_agente`, `agentes_ativos`, `listar_templates`, `instanciar_template`, `listar_origens`, `criar_origem`, `criar_lembrete`, `proximos_lembretes`, `adiar_lembrete`, `alterar_lembrete`, `confirmar_lembrete`, `cancelar_lembrete`, `aguardar_resposta`, `retomar_trabalho`,
`voz_panorama`, `voz_situacao`, `voz_enviar`, `voz_tecla`.

## Lançador de agentes (opcional)

Com `rex servir -lancador lancador.json`, uma tarefa que entra em `em_andamento` (pelo frontend
ou por quem não é uma sessão) ganha um agente numa sessão tmux; uma tarefa que sai de `em_andamento`
por mão de outro faz o agente vivo encerrar a sessão com resumo. Depois de encerrar, o próprio
agente fecha o seu tmux (skill `rex`).

O nome do tmux (e do remote-control, via `{nome}` no comando) é o `nome` dado ao lançar; sem ele, sai do
título, sem verbos genéricos. O agente troca o próprio nome com `renomear_sessao`: o lançador renomeia o
tmux e manda `/rename` ao Claude Code no painel dele.

```json
{
  "diretorio": "/caminho/dos/journals",
  "comando": ["claude", "--remote-control", "{nome}"],
  "modelos": [
    {"nome": "Modelo A", "argumentos": ["--model", "modelo-a"]},
    {"nome": "Modelo B", "argumentos": ["--model", "modelo-b"]}
  ],
  "esforcos": ["low", "medium", "high"],
  "argumentos_esforco": ["--effort", "{esforco}"],
  "esforco_padrao": "high",
  "remover_do_ambiente": ["CLAUDECODE", "CLAUDE_EFFORT", "CLAUDE_CODE_SESSION_ID"]
}
```

`remover_do_ambiente` evita que o agente herde variáveis do servidor tmux: se o tmux foi aberto de
dentro de outro agente, o novo herdaria o esforço e o chat dele.

No frontend, pôr uma tarefa em andamento abre a escolha de modelo, esforço e uma instrução adicional
de reabertura (`POST /tarefas/{id}/lancamento`); o primeiro modelo é o padrão.

`prompt_iniciar` e `prompt_encerrar` são opcionais e aceitam `{tarefa}`, `{titulo}` e `{status}`.
No systemd, use `KillMode=process` para reiniciar o servidor sem derrubar o tmux dos agentes.

O MCP, com `rex mcp -journals /caminho/dos/journals`, cria a pasta de cada sessão
(`S-0001/journal.md`) ao iniciá-la e registra o localizador.

## Login pelo Google (opcional)

Com `-endereco-com-login` e `-login login.json`, os endereços expostos ao navegador exigem login
pelo Google, liberado só para os e-mails da configuração; os de `-endereco` (loopback, para o MCP,
o lançador e as integrações) continuam abertos. Configuração e HTTPS em [`docs/login.md`](docs/login.md).

## Lembretes e integrações (opcional)

Lembretes (`/lembretes`) têm hora marcada e são disparados pelo próprio `rex servir` (a cada
15 s; os que venceram com o servidor parado disparam ao subir, marcados como atrasados). O rex
não fala com celular, barra de status nem assistente de voz: com `-integracoes integracoes.json`,
cada instalação liga ganchos (comando ou webhook) aos eventos e consulta
`GET /integracao/pendencias` por polling. Desenho, configuração e garantias em
[`docs/integracao.md`](docs/integracao.md).

## Desenvolver

```bash
go generate ./api   # regenera api/api.gen.go a partir do contrato OpenAPI
go test ./...
```

Frontend (React + Vite + TypeScript + Tailwind + shadcn/ui), embutido no binário a partir de `web/dist`:

```bash
cd web
npm ci
npm run gerar   # tipos TypeScript a partir do contrato OpenAPI
npm run build   # compila para web/dist; depois recompile o binário Go
REX_API=http://127.0.0.1:8787 npm run dev   # desenvolvimento, com a API encaminhada
```

O frontend fica em `/` e navega pelo hash da URL (`/#/tarefas/T-0001`); os caminhos `/tarefas`,
`/sessoes`, `/eventos`, `/importacao`, `/lancador`, `/lembretes` e `/integracao` são da API.
