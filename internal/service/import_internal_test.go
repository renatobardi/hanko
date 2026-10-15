package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/export"
)

// A ordenação das tasks é O(n log n): um arquivo grande em ordem inversa não pode travar a importação
// (insertion sort levaria minutos com este tamanho).
func TestOrderTasksGrandeEmOrdemInversa(t *testing.T) {
	const n = 300_000
	tasks := make([]export.Task, n)
	byNum := make(map[int64]int, n)
	for i := range tasks {
		tasks[i] = export.Task{Number: int64(n - i)}
		byNum[tasks[i].Number] = i
	}
	start := time.Now()
	order, err := orderTasks(tasks, byNum)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("orderTasks levou %v para %d tasks", d, n)
	}
	for i, idx := range order {
		if tasks[idx].Number != int64(i+1) {
			t.Fatalf("posição %d: task %d", i, tasks[idx].Number)
		}
	}
}

// Estável: números iguais não existem (duplicata é recusada antes), mas pais continuam antes dos filhos.
func TestOrderTasksPaiAntesDoFilho(t *testing.T) {
	tasks := []export.Task{{Number: 1, Parent: 3}, {Number: 2}, {Number: 3}}
	byNum := map[int64]int{1: 0, 2: 1, 3: 2}
	order, err := orderTasks(tasks, byNum)
	if err != nil {
		t.Fatal(err)
	}
	pos := map[int64]int{}
	for i, idx := range order {
		pos[tasks[idx].Number] = i
	}
	if pos[3] > pos[1] {
		t.Fatalf("pai depois do filho: %v", pos)
	}
}

// A validação respeita o contexto: um pedido cancelado não segue validando um arquivo gigante.
func TestPlanImportRespeitaContexto(t *testing.T) {
	doc := export.Document{Format: export.Format, Company: export.Company{Name: "X"}}
	for i := 1; i <= 50; i++ {
		doc.Tasks = append(doc.Tasks, export.Task{Number: int64(i), Title: "t", Status: "todo", Priority: "low", CreatedBy: export.Actor{Type: "human"}})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&Service{}).planImport(ctx, doc, ImportOptions{Slug: "x"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("esperava context.Canceled, veio %v", err)
	}
	if _, err := (&Service{}).planImport(context.Background(), doc, ImportOptions{Slug: "x"}); err != nil {
		t.Fatalf("arquivo válido: %v", err)
	}
}
