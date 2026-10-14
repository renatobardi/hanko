package procrun

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunLinesAndExit(t *testing.T) {
	var mu sync.Mutex
	var out, errs []string
	res, err := Run(context.Background(), Options{
		Bin: "bash", Args: []string{"-c", `read x; echo "got:$x"; echo a; echo b >&2; printf 'tail'; exit 3`},
		Stdin:    "hello\n",
		OnStdout: func(l []byte) { mu.Lock(); out = append(out, string(l)); mu.Unlock() },
		OnStderr: func(l []byte) { mu.Lock(); errs = append(errs, string(l)); mu.Unlock() },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || res.Cancelled {
		t.Fatalf("res=%+v", res)
	}
	if strings.Join(out, "|") != "got:hello|a|tail" || strings.Join(errs, "|") != "b" {
		t.Fatalf("out=%v errs=%v", out, errs)
	}
}

func TestRunStartError(t *testing.T) {
	if _, err := Run(context.Background(), Options{Bin: "/nonexistent/bin-xyz"}); err == nil {
		t.Fatal("want error")
	}
}

func TestRunCancelKillsGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 1)
	go func() {
		<-started
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	t0 := time.Now()
	// The child ignores SIGTERM and spawns a grandchild; both must die.
	res, err := Run(ctx, Options{
		Bin: "bash", Args: []string{"-c", `trap '' TERM; sleep 30 & echo up; wait`},
		Grace: 300 * time.Millisecond,
		OnStdout: func([]byte) {
			select {
			case started <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Cancelled {
		t.Fatalf("want cancelled: %+v", res)
	}
	if d := time.Since(t0); d > 5*time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestLongLineTruncated(t *testing.T) {
	var got int
	_, err := Run(context.Background(), Options{
		Bin: "bash", Args: []string{"-c", `head -c 20000000 /dev/zero | tr '\0' x; echo`},
		OnStdout: func(l []byte) { got = len(l) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != MaxLine {
		t.Fatalf("len=%d", got)
	}
}
