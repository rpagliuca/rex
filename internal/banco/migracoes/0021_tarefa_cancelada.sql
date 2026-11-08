-- rex:sem-chaves-estrangeiras
-- Status cancelada: a tarefa que não queremos mais fazer. O SQLite não altera um CHECK: a tabela é
-- recriada pelo procedimento oficial (https://sqlite.org/lang_altertable.html#otheralter). Cinco
-- tabelas referenciam tarefas, então o executor roda esta migração com as chaves estrangeiras
-- desligadas (o marcador acima) e confere todas elas antes do commit (foreign_key_check).

CREATE TABLE tarefas_novo (
    id            INTEGER PRIMARY KEY,
    titulo        TEXT NOT NULL CHECK (length(trim(titulo)) > 0),
    descricao     TEXT NOT NULL DEFAULT '',
    organizacao   TEXT NOT NULL CHECK (length(trim(organizacao)) > 0),
    status        TEXT NOT NULL DEFAULT 'a_fazer'
                  CHECK (status IN ('a_fazer', 'em_andamento', 'pausada', 'bloqueada', 'em_validacao', 'concluida', 'cancelada')),
    resumo        TEXT NOT NULL DEFAULT '',
    criada_em     TEXT NOT NULL,
    atualizada_em TEXT NOT NULL,
    template_id   INTEGER REFERENCES templates (id)
);

INSERT INTO tarefas_novo (id, titulo, descricao, organizacao, status, resumo, criada_em, atualizada_em, template_id)
SELECT id, titulo, descricao, organizacao, status, resumo, criada_em, atualizada_em, template_id FROM tarefas;

DROP TABLE tarefas;
ALTER TABLE tarefas_novo RENAME TO tarefas;
CREATE INDEX tarefas_por_template ON tarefas (template_id);
