package service

import "time"

// SetNativeGatePoll encurta o intervalo com que AwaitNativeToolGate consulta o gate (só para os testes).
func SetNativeGatePoll(d time.Duration) { nativeGatePoll = d }
