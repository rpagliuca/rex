-- Grau de aviso do lembrete de hora marcada: até onde as integrações escalam antes da hora.
-- 1 só a ilha do iPhone; 2 também a notificação silenciosa e a lâmpada (padrão); 3 também o alarme
-- com som. Nos lembretes de turno, dia, semana, fim de semana e mês não vale: esses nunca tocam.
ALTER TABLE lembretes ADD COLUMN aviso_max INTEGER NOT NULL DEFAULT 2 CHECK (aviso_max BETWEEN 1 AND 3);
