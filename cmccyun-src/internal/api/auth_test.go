package api

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ticket builds the browser ticket shape the client has to deal with:
// base64("pc:<account>:<token>|<field>|<field>|<expiry-ms>").
func ticket(account, token string, expiry time.Time) string {
	blob := fmt.Sprintf("pc:%s:%s|1|2|%d", account, token, expiry.UnixMilli())
	return base64.StdEncoding.EncodeToString([]byte(blob))
}

func refreshServer(t *testing.T, wantTicket string, newToken string, calls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			*calls++
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "<token>"+wantTicket+"</token>") &&
			!strings.Contains(string(body), "<token>"+strings.SplitN(wantTicket, "|", 2)[0]+"</token>") {
			fmt.Fprintf(w, `<root><return>1</return><desc>bad ticket</desc><token></token></root>`)
			return
		}
		fmt.Fprintf(w, `<root><return>0</return><desc></desc><token>%s</token></root>`, newToken)
	}))
}

// TestEnsureFreshRefreshesAndPersists checks the whole token-lifecycle: a token
// close to expiry is renewed through the AAS endpoint, the expiry is picked up
// from the new token, and the new Authorization reaches the persistence hook.
func TestEnsureFreshRefreshesAndPersists(t *testing.T) {
	newExpiry := time.Now().Add(30 * 24 * time.Hour)
	newToken := "newtok|1|2|" + strconv.FormatInt(newExpiry.UnixMilli(), 10)
	srv := refreshServer(t, "oldtok|1|2|x", newToken, nil)
	defer srv.Close()

	expiring := time.Now().Add(5 * 24 * time.Hour)
	c, err := New(ticket("13800138000", "oldtok", expiring))
	if err != nil {
		t.Fatal(err)
	}
	c.SetRefreshURL(srv.URL)
	var persisted string
	c.SetRefreshFunc(func(auth string) { persisted = auth })

	if st := c.AuthState(); st.ExpiresDays < 4 || st.ExpiresDays > 6 {
		t.Fatalf("unexpected expiry from the pasted ticket: %v days", st.ExpiresDays)
	}
	if err := c.EnsureFresh(context.Background()); err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}
	st := c.AuthState()
	if st.ExpiresDays < 29 || st.ExpiresDays > 31 {
		t.Fatalf("expiry was not taken from the refreshed token: %v days", st.ExpiresDays)
	}
	if st.LastRefresh.IsZero() {
		t.Fatal("LastRefresh not recorded")
	}
	if st.LastError != "" {
		t.Fatalf("LastError=%q", st.LastError)
	}
	raw, err := base64.StdEncoding.DecodeString(persisted)
	if err != nil {
		t.Fatalf("persistence hook got a non base64 value %q: %v", persisted, err)
	}
	if !strings.HasPrefix(string(raw), "pc:13800138000:newtok") {
		t.Fatalf("persisted credential = %q", raw)
	}
	if got := c.Authorization(); got != persisted {
		t.Fatalf("Authorization() = %q want %q", got, persisted)
	}
}

// TestRefreshFallsBackToBareToken covers the second AAS deployment shape: the
// endpoint rejects the whole "|"-separated blob and wants the leading token.
func TestRefreshFallsBackToBareToken(t *testing.T) {
	newExpiry := time.Now().Add(30 * 24 * time.Hour)
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, string(body))
		if strings.Contains(string(body), "<token>oldtok|") {
			fmt.Fprintf(w, `<root><return>1</return><desc>unsupported</desc></root>`)
			return
		}
		fmt.Fprintf(w, `<root><return>0</return><token>fresh|1|2|%d</token></root>`, newExpiry.UnixMilli())
	}))
	defer srv.Close()

	c, err := New(ticket("13800138000", "oldtok", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	c.SetRefreshURL(srv.URL)
	if err := c.EnsureFresh(context.Background()); err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("expected the blob then the bare token, saw %d request(s)", len(seen))
	}
	if !strings.Contains(seen[0], "<token>oldtok|1|2|") {
		t.Fatalf("first attempt should post the whole ticket: %s", seen[0])
	}
	if !strings.Contains(seen[1], "<token>oldtok</token>") {
		t.Fatalf("second attempt should post the bare token: %s", seen[1])
	}
	if d := c.AuthState().ExpiresDays; d < 29 {
		t.Fatalf("token was not renewed: %v days", d)
	}
}

// TestEnsureFreshLeavesHealthyTokenAlone makes sure the 6h background loop does
// not hammer the endpoint when the token is still good.
func TestEnsureFreshLeavesHealthyTokenAlone(t *testing.T) {
	calls := 0
	srv := refreshServer(t, "ignored", "x", &calls)
	defer srv.Close()

	c, err := New(ticket("13800138000", "oldtok", time.Now().Add(60*24*time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	c.SetRefreshURL(srv.URL)
	if err := c.EnsureFresh(context.Background()); err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}
	if calls != 0 {
		t.Fatalf("refreshed a token that still had 60 days left (%d calls)", calls)
	}
}

// TestEnsureFreshRejectsExpiredToken: once the token is gone the user has to
// paste a fresh Authorization, so no network call is attempted.
func TestEnsureFreshRejectsExpiredToken(t *testing.T) {
	calls := 0
	srv := refreshServer(t, "ignored", "x", &calls)
	defer srv.Close()

	c, err := New(ticket("13800138000", "oldtok", time.Now().Add(-time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	c.SetRefreshURL(srv.URL)
	err = c.EnsureFresh(context.Background())
	if err == nil {
		t.Fatal("expected an error for an expired token")
	}
	if calls != 0 {
		t.Fatalf("expired token should not be refreshed remotely (%d calls)", calls)
	}
	if !strings.Contains(c.AuthState().LastError, "过期") {
		t.Fatalf("LastError=%q should tell the user to paste a new Authorization", c.AuthState().LastError)
	}
}

// TestRefreshFailureIsReported checks that a rejected refresh surfaces a usable
// message instead of silently keeping a dead token.
func TestRefreshFailureIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<root><return>1</return><desc>令牌无效</desc></root>`)
	}))
	defer srv.Close()

	c, err := New(ticket("13800138000", "oldtok", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	c.SetRefreshURL(srv.URL)
	if err := c.EnsureFresh(context.Background()); err == nil {
		t.Fatal("expected an error when the service rejects both ticket shapes")
	}
	if !strings.Contains(c.AuthState().LastError, "刷新令牌失败") {
		t.Fatalf("LastError=%q", c.AuthState().LastError)
	}
}

// TestSetAuthorizationWithoutToken clears the credential cleanly.
func TestSetAuthorizationWithoutToken(t *testing.T) {
	c, err := New(ticket("13800138000", "tok", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Ready() {
		t.Fatal("client should be ready after a successful SetAuthorization")
	}
	if err := c.SetAuthorization(""); err != ErrNoAuth {
		t.Fatalf("SetAuthorization(\"\") = %v want ErrNoAuth", err)
	}
	if c.Ready() || c.AuthState().HasToken {
		t.Fatal("credential was not cleared")
	}
	if err := c.EnsureFresh(context.Background()); err != ErrNoAuth {
		t.Fatalf("EnsureFresh without credential = %v want ErrNoAuth", err)
	}
}
