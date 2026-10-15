package service

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// SearchTxForTest expõe searchTx (transação da busca com statement_timeout) aos testes de service_test.
func (s *Service) SearchTxForTest(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return s.searchTx(ctx, fn)
}
