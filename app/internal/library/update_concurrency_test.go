package library

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestConcurrentRequestsReadProxyWithoutRaces(t *testing.T) {
	p := proxyFor(" http://127.0.0.1:7890 ", func() string { return "" })
	req, _ := http.NewRequest("GET", "https://api.github.com/", nil)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = p(req)
			}
		}()
	}
	wg.Wait()
}
func TestConcurrentRecordCleanupPreservesAllChanges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", "")
	must(t, os.MkdirAll(filepath.Join(home, ".agents"), 0755))
	must(t, os.WriteFile(lockFile(home), []byte(bigLock(32)), 0600))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		name := fmt.Sprintf("skill-%02d", i)
		if i == 16 {
			name = "vite"
		}
		l := Scan(home)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			n, err := l.ClearRecords(StaleRecord{Names: []string{name}})
			if err != nil || n != 1 {
				t.Errorf("cleanup %s: n=%d err=%v", name, n, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	r, err := ReadInstallRecords(home)
	must(t, err)
	if len(r.Entries) != 0 {
		t.Fatalf("all 32 cleanups reported success, but %d records remain", len(r.Entries))
	}
}

func TestThrottleDoesNotRetryBeforeServerDeadline(t *testing.T) {
	calls := 0
	g := testGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(429)
	})
	var waited time.Duration
	g.sleep = func(_ context.Context, d time.Duration) error { waited += d; return nil }
	_, _ = g.Commit(context.Background(), "a/b", "")
	if calls > 1 && waited < 120*time.Second {
		t.Fatalf("retried after %s despite Retry-After: 120", waited)
	}
}
