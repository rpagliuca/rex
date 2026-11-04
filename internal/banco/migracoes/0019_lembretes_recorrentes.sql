-- Lembretes recorrentes. Cada ocorrência continua sendo um lembrete comum (as integrações não mudam);
-- a série é a corrente de ocorrências ligadas por seguinte_id, todas com o mesmo serie_id (o id da
-- primeira). A próxima ocorrência é serie_inicio + (ocorrencia + 1) · intervalo, gerada quando a
-- atual dispara ou é confirmada; adiar uma ocorrência não desloca as seguintes.
ALTER TABLE lembretes ADD COLUMN recorrencia TEXT CHECK (recorrencia IN ('diaria', 'semanal', 'mensal', 'anual'));
ALTER TABLE lembretes ADD COLUMN intervalo INTEGER NOT NULL DEFAULT 1 CHECK (intervalo >= 1);
-- Âncora da série (RFC 3339 com o fuso em que foi pedida) e a posição desta ocorrência nela.
ALTER TABLE lembretes ADD COLUMN serie_inicio TEXT;
ALTER TABLE lembretes ADD COLUMN ocorrencia INTEGER NOT NULL DEFAULT 0;
ALTER TABLE lembretes ADD COLUMN serie_id INTEGER REFERENCES lembretes (id);
ALTER TABLE lembretes ADD COLUMN seguinte_id INTEGER REFERENCES lembretes (id);
