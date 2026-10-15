//go:build integration

package heartbeat

import "github.com/renatobardi/hanko/internal/auth"

func authForTest() auth.RunTokens { return auth.RunTokens{Secret: []byte("0123456789abcdef0123")} }
