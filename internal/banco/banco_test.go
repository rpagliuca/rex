package banco

import (
	"path/filepath"
	"testing"
)

// A 0021 recria tarefas com as chaves estrangeiras desligadas: depois dela, cancelada é aceito, as
// referências continuam valendo e as chaves voltam a ser impostas.
func TestTarefaCanceladaMantemAsChavesEstrangeiras(t *testing.T) {
	db, err := Abrir(filepath.Join(t.TempDir(), "rex.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var ligadas int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&ligadas); err != nil || ligadas != 1 {
		t.Fatalf("chaves estrangeiras desligadas depois das migrações: %d %v", ligadas, err)
	}
	if _, err := db.Exec(`INSERT INTO tarefas (titulo, organizacao, status, criada_em, atualizada_em)
		VALUES ('Desistimos', 'Exemplo', 'cancelada', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("cancelada recusada: %v", err)
	}
	if _, err := db.Exec(`UPDATE tarefas SET status = 'terminada'`); err == nil {
		t.Fatal("status fora da lista aceito")
	}
	if _, err := db.Exec(`INSERT INTO tarefa_componente (tarefa_id, componente_id) VALUES (999, 999)`); err == nil {
		t.Fatal("referência a tarefa inexistente aceita")
	}
}
