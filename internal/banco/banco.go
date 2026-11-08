// Package banco abre o SQLite do rex e aplica as migrações versionadas.
package banco

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migracoes/*.sql
var migracoes embed.FS

// Abrir abre (ou cria) o banco no caminho dado e aplica as migrações pendentes.
// Use ":memory:" para um banco efêmero.
func Abrir(caminho string) (*sql.DB, error) {
	dsn := "file:" + caminho + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	if caminho != ":memory:" {
		dsn += "&_pragma=journal_mode(WAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Um único escritor: o servidor serializa as escritas e evita SQLITE_BUSY.
	// Também mantém o mesmo banco quando ele é ":memory:".
	db.SetMaxOpenConns(1)
	if err := migrar(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// semChavesEstrangeiras marca, na primeira linha, a migração que recria uma tabela referenciada por
// outras: ela roda com as chaves estrangeiras desligadas (o PRAGMA não muda dentro de uma transação)
// e só é confirmada se foreign_key_check não achar nenhuma referência quebrada.
const semChavesEstrangeiras = "-- rex:sem-chaves-estrangeiras"

func migrar(db *sql.DB) error {
	ctx := context.Background()
	// Uma conexão fixa: os PRAGMAs valem por conexão.
	c, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS migracoes (nome TEXT PRIMARY KEY)`); err != nil {
		return err
	}
	nomes, err := fs.Glob(migracoes, "migracoes/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(nomes)
	for _, nome := range nomes {
		var aplicada int
		if err := c.QueryRowContext(ctx, `SELECT count(*) FROM migracoes WHERE nome = ?`, nome).Scan(&aplicada); err != nil {
			return err
		}
		if aplicada > 0 {
			continue
		}
		conteudo, err := migracoes.ReadFile(nome)
		if err != nil {
			return err
		}
		if err := aplicar(ctx, c, nome, string(conteudo)); err != nil {
			return err
		}
	}
	return nil
}

func aplicar(ctx context.Context, c *sql.Conn, nome, conteudo string) (err error) {
	semChaves := strings.HasPrefix(conteudo, semChavesEstrangeiras)
	if semChaves {
		if _, err := c.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			return err
		}
		defer func() {
			if _, e := c.ExecContext(ctx, `PRAGMA foreign_keys = ON`); e != nil && err == nil {
				err = e
			}
		}()
	}
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(conteudo); err != nil {
		tx.Rollback()
		return fmt.Errorf("migração %s: %w", nome, err)
	}
	if semChaves {
		linhas, err := tx.Query(`PRAGMA foreign_key_check`)
		if err != nil {
			tx.Rollback()
			return err
		}
		quebrada := linhas.Next()
		linhas.Close()
		if quebrada {
			tx.Rollback()
			return fmt.Errorf("migração %s: deixaria referências quebradas (foreign_key_check)", nome)
		}
	}
	if _, err := tx.Exec(`INSERT INTO migracoes (nome) VALUES (?)`, nome); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
