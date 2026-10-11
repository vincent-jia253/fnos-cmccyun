package app

// Regression test for the callback/lock contract: the api client invokes its
// refresh callback from inside a request, and requests can happen while the
// service mutex is held (Mount → Init). If that callback takes the service lock
// synchronously, the first forced renewal deadlocks the daemon — which is
// exactly what the on-box auth-recovery demo caught.

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vincent-jia253/fnos-cmccyun/internal/config"
)

// ticket builds the base64("pc:<acct>:<token>") value a user pastes.
func ticket(token string) string {
	return base64.StdEncoding.EncodeToString(
		[]byte("pc:13800138000:" + token + "|||" + fmt.Sprint(time.Now().Add(24*time.Hour).UnixMilli())))
}

func TestRefreshCallbackDoesNotDeadlockUnderServiceLock(t *testing.T) {
	// The refresh endpoint returns the bare token part; the client rebuilds
	// base64("<scheme>:<acct>:<token>") around it.
	newToken := "NEWTOK|||" + fmt.Sprint(time.Now().Add(30*24*time.Hour).UnixMilli())
	wantAuth := base64.StdEncoding.EncodeToString([]byte("pc:13800138000:" + newToken))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, "<root><return>0</return><desc></desc><token>%s</token></root>", newToken)
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := config.Default()
	cfg.Authorization = ticket("OLDTOK")
	cfg.MountPoint = filepath.Join(dir, "mnt")
	cfg.StageDir = filepath.Join(dir, "staging")
	cfg.LogFile = filepath.Join(dir, "cmccyun.log")
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	s, err := New(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.newClient(cfg.Authorization)
	if err != nil {
		t.Fatal(err)
	}
	c.SetRefreshURL(srv.URL)
	c.SetRouteBase(srv.URL)

	// Stand in for Mount(): the service lock is held while a request runs.
	s.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- c.RefreshToken(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			s.mu.Unlock()
			t.Fatalf("RefreshToken: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("续期回调在持有服务锁时被阻塞（同步回调 → 死锁）")
	}
	s.mu.Unlock()

	// The credential must still be persisted (the callback runs off-lock).
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := config.Load(cfgPath)
		if err == nil {
			if got.Authorization == wantAuth {
				return
			}
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(cfgPath)
			t.Fatalf("新令牌未写回配置: %s", strings.TrimSpace(string(data)))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// A request while mounting must not freeze the console: Mount() talks to the
// cloud (token check + route lookup, and possibly a renewal), and it must do so
// with the service lock released. This is what makes /api/status keep answering
// when the cloud is slow or unreachable.
func TestConnectDoesNotHoldServiceLock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(700 * time.Millisecond) // pretend the cloud is slow
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"code":"0","message":"成功","data":{"routePolicyList":[`+
			`{"modName":"personal","httpsUrl":"http://127.0.0.1:1"}]}}`)
	}))
	defer srv.Close()
	t.Setenv("CMCCYUN_ROUTE_BASE", srv.URL)
	t.Setenv("CMCCYUN_DEBUG_FAKE", "")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := config.Default()
	cfg.Authorization = ticket("TOK")
	cfg.MountPoint = filepath.Join(dir, "mnt")
	cfg.StageDir = filepath.Join(dir, "staging")
	cfg.LogFile = filepath.Join(dir, "cmccyun.log")
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	s, err := New(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, cerr := s.connect(context.Background())
		done <- cerr
	}()

	// While connect() is in flight the lock must still be available.
	acquired := make(chan struct{})
	go func() {
		s.mu.Lock()
		close(acquired)
		s.mu.Unlock()
	}()
	select {
	case <-acquired:
	case <-time.After(400 * time.Millisecond):
		t.Fatal("connect() 在持服务锁的情况下做网络请求：控制台会被云端拖住")
	}
	<-done
}
