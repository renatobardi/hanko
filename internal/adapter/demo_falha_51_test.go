package adapter

import "testing"

// Teste que falha de propósito (#51): prova que um shard vermelho deixa o check vermelho. Não vai para a main.
func TestDemoFalhaShard51(t *testing.T) {
	t.Fatal("falha proposital (#51)")
}
