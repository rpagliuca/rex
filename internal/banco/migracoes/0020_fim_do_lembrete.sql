-- Precisão fim_de_semana (sábado e domingo) e o fim da janela gravado: o rex calcula o fim ao salvar
-- (semana até sexta no início da noite, mês até o último dia útil), em vez de recalcular a cada
-- leitura. O fim dos lembretes existentes é preenchido pelo servidor ao subir (PreencherFins), com a
-- regra nova. O SQLite não altera um CHECK: a tabela é recriada. As referências da série apontam
-- para a própria tabela nova (o RENAME as reescreve) e são copiadas depois de todas as linhas, para
-- seguinte_id não apontar para uma linha ainda não copiada.

CREATE TABLE lembretes_novo (
    id            INTEGER PRIMARY KEY,
    texto         TEXT NOT NULL CHECK (length(trim(texto)) > 0),
    -- Instante com o fuso em que foi pedido (RFC 3339); quando_utc ordena e compara.
    quando        TEXT NOT NULL,
    quando_utc    TEXT NOT NULL,
    organizacao   TEXT NOT NULL CHECK (length(trim(organizacao)) > 0),
    tarefa_id     INTEGER REFERENCES tarefas (id),
    requisito_id  INTEGER REFERENCES requisitos (id),
    status        TEXT NOT NULL DEFAULT 'agendado'
                  CHECK (status IN ('agendado', 'disparado', 'adiado', 'confirmado', 'cancelado')),
    autor         TEXT NOT NULL,
    disparado_em  TEXT,
    atrasado      INTEGER NOT NULL DEFAULT 0 CHECK (atrasado IN (0, 1)),
    adiamentos    INTEGER NOT NULL DEFAULT 0,
    confirmado_em TEXT,
    criado_em     TEXT NOT NULL,
    atualizado_em TEXT NOT NULL,
    precisao      TEXT NOT NULL DEFAULT 'hora'
                  CHECK (precisao IN ('hora', 'turno', 'dia', 'semana', 'fim_de_semana', 'mes')),
    curto         TEXT,
    recorrencia   TEXT CHECK (recorrencia IN ('diaria', 'semanal', 'mensal', 'anual')),
    intervalo     INTEGER NOT NULL DEFAULT 1 CHECK (intervalo >= 1),
    serie_inicio  TEXT,
    ocorrencia    INTEGER NOT NULL DEFAULT 0,
    serie_id      INTEGER REFERENCES lembretes_novo (id),
    seguinte_id   INTEGER REFERENCES lembretes_novo (id),
    -- Fim (exclusivo) da janela, com o fuso de quando; NULL na precisão hora. fim_utc compara.
    fim           TEXT,
    fim_utc       TEXT,
    CHECK ((status = 'confirmado') = (confirmado_em IS NOT NULL)),
    CHECK ((fim IS NULL) = (fim_utc IS NULL))
);

INSERT INTO lembretes_novo (id, texto, quando, quando_utc, organizacao, tarefa_id, requisito_id, status, autor,
    disparado_em, atrasado, adiamentos, confirmado_em, criado_em, atualizado_em, precisao, curto,
    recorrencia, intervalo, serie_inicio, ocorrencia)
SELECT id, texto, quando, quando_utc, organizacao, tarefa_id, requisito_id, status, autor,
    disparado_em, atrasado, adiamentos, confirmado_em, criado_em, atualizado_em, precisao, curto,
    recorrencia, intervalo, serie_inicio, ocorrencia
FROM lembretes;

UPDATE lembretes_novo SET
    serie_id = (SELECT l.serie_id FROM lembretes l WHERE l.id = lembretes_novo.id),
    seguinte_id = (SELECT l.seguinte_id FROM lembretes l WHERE l.id = lembretes_novo.id);

DROP TABLE lembretes;
ALTER TABLE lembretes_novo RENAME TO lembretes;
CREATE INDEX lembretes_pendentes ON lembretes (quando_utc) WHERE status IN ('agendado', 'adiado', 'disparado');
