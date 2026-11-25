-- rex voz (atalho "rex" da Siri): a sessão associada e, em cada sessão, o ponteiro do que o usuário
-- já ouviu (uuid de uma linha do transcript do agente) e quantos pedidos ele mandou por voz.
-- `proposto` é o ponteiro entregue ao atalho e ainda não confirmado.
CREATE TABLE voz (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	sessao INTEGER REFERENCES sessoes(id),
	proposto TEXT
);
INSERT INTO voz (id) VALUES (1);
ALTER TABLE sessoes ADD COLUMN voz_lido_ate TEXT;
ALTER TABLE sessoes ADD COLUMN voz_pedidos INTEGER NOT NULL DEFAULT 0;
