-- Palavra-chave do lembrete (até 10 caracteres) para os resumos de pouco espaço, como a ilha do
-- iPhone. Opcional: sem ela, as integrações usam o começo do texto.
ALTER TABLE lembretes ADD COLUMN curto TEXT;
