package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ticketFor builds the base64("pc:<account>:<token>") value a user pastes,
// carrying an expiry in the 4th "|" field like the real service does.
func ticketFor(token string, expires time.Time) string {
	tok := token + "|||" + strconv.FormatInt(expires.UnixMilli(), 10)
	return base64.StdEncoding.EncodeToString([]byte("pc:13800138000:" + tok))
}

func basic(auth string) string { return "Basic " + auth }

// fakeDrive is a stand-in personal cloud: it only answers /api/echo when the
// request carries an Authorization value it considers valid.
type fakeDrive struct {
	mu       sync.Mutex
	valid    map[string]bool
	refreshN atomic.Int32
	newToken string

	// businessPath, when set, returns a business-level error instead.
	businessPath string

	// refreshFail makes /refresh answer with that status code.
	refreshFail int

	// routeURL is handed out by the account-route lookup.
	routeURL string

	rejected []string
}

func newFakeDrive(t *testing.T, valid ...string) (*httptest.Server, *fakeDrive) {
	t.Helper()
	d := &fakeDrive{valid: map[string]bool{}}
	for _, v := range valid {
		d.valid[v] = true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/refresh", func(w http.ResponseWriter, r *http.Request) {
		d.refreshN.Add(1)
		io.Copy(io.Discard, r.Body)
		d.mu.Lock()
		fail := d.refreshFail
		d.mu.Unlock()
		if fail != 0 {
			w.WriteHeader(fail)
			fmt.Fprint(w, "<root><return>1</return><desc>busy</desc></root>")
			return
		}
		d.mu.Lock()
		d.valid[d.newToken] = true
		d.mu.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, "<root><return>0</return><desc></desc><token>%s</token></root>",
			tokenPart(d.newToken))
	})
	mux.HandleFunc("/user/route/qryRoutePolicy", func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Basic ")
		d.mu.Lock()
		ok, u := d.valid[auth], d.routeURL
		d.mu.Unlock()
		if !ok {
			writeJSON(w, map[string]any{"success": false, "code": "10001", "message": "登录状态已失效，请重新登录"})
			return
		}
		writeJSON(w, map[string]any{"success": true, "code": "0", "message": "成功",
			"data": map[string]any{"routePolicyList": []map[string]any{
				{"modName": "personal", "httpsUrl": u}}}})
	})
	mux.HandleFunc("/api/echo", func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Basic ")
		d.mu.Lock()
		ok := d.valid[auth]
		d.mu.Unlock()
		if !ok {
			d.mu.Lock()
			d.rejected = append(d.rejected, auth)
			d.mu.Unlock()
			writeJSON(w, map[string]any{"success": false, "code": "10001", "message": "登录状态已失效，请重新登录"})
			return
		}
		if d.businessPath == r.URL.Path {
			writeJSON(w, map[string]any{"success": false, "code": "200000408", "message": "同名文件已存在"})
			return
		}
		writeJSON(w, map[string]any{"success": true, "code": "0", "message": "成功",
			"data": map[string]any{"echo": "hi"}})
	})
	srv := httptest.NewServer(mux)
	d.newToken = ticketFor("NEW", time.Now().Add(30*24*time.Hour))
	d.routeURL = srv.URL
	return srv, d
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// tokenPart extracts base64("pc:acct:<token>") back into its token part so the
// fake refresh endpoint can hand out a value in the shape the client expects.
func tokenPart(auth string) string {
	raw, err := base64.StdEncoding.DecodeString(auth)
	if err != nil {
		return auth
	}
	parts := strings.SplitN(string(raw), ":", 3)
	if len(parts) < 3 {
		return auth
	}
	return parts[2]
}

func echoPost(t *testing.T, c *Client) (string, error) {
	t.Helper()
	var out struct {
		Data struct {
			Echo string `json:"echo"`
		} `json:"data"`
	}
	err := c.post(context.Background(), "/api/echo", map[string]any{"hello": 1}, &out)
	return out.Data.Echo, err
}

// TestPostRecoversFromAuthFailure is the regression test for the on-demand
// credential recovery: the service rejects the ticket, the client renews it and
// replays the request instead of leaving the mount broken until the next sweep.
func TestPostRecoversFromAuthFailure(t *testing.T) {
	old := ticketFor("OLD", time.Now().Add(20*24*time.Hour))
	srv, drive := newFakeDrive(t)
	defer srv.Close()

	c, err := New(old)
	if err != nil {
		t.Fatal(err)
	}
	c.SetRefreshURL(srv.URL + "/refresh")
	c.SetRouteBase(srv.URL) // keep the route lookup off the public internet
	c.host = srv.URL

	echo, err := echoPost(t, c)
	if err != nil {
		t.Fatalf("post after recovery: %v", err)
	}
	if echo != "hi" {
		t.Fatalf("echo=%q", echo)
	}
	if n := drive.refreshN.Load(); n != 1 {
		t.Fatalf("refresh calls=%d want 1", n)
	}
	if got := c.Authorization(); got != drive.newToken {
		t.Fatalf("credential not replaced: %q", got)
	}
	if st := c.AuthState(); st.LastError != "" {
		t.Fatalf("LastError should be cleared after a good refresh: %q", st.LastError)
	}
}

// TestPostDoesNotRefreshOnBusinessError keeps the recovery narrow: a rejected
// request (bad name, missing file…) must not cost a token refresh.
func TestPostDoesNotRefreshOnBusinessError(t *testing.T) {
	old := ticketFor("OLD", time.Now().Add(20*24*time.Hour))
	srv, drive := newFakeDrive(t)
	defer srv.Close()
	drive.valid[old] = true // the credential is fine; the request itself is not
	drive.businessPath = "/api/echo"

	c, err := New(old)
	if err != nil {
		t.Fatal(err)
	}
	c.SetRefreshURL(srv.URL + "/refresh")
	c.SetRouteBase(srv.URL) // keep the route lookup off the public internet
	c.host = srv.URL

	_, err = echoPost(t, c)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != "200000408" {
		t.Fatalf("err=%v want APIError 200000408", err)
	}
	if n := drive.refreshN.Load(); n != 0 {
		t.Fatalf("refresh calls=%d want 0", n)
	}
}

// TestConcurrentAuthFailureRefreshesOnce checks the recovery stays cheap when a
// busy mount has many requests in flight: exactly one refresh, all succeed.
func TestConcurrentAuthFailureRefreshesOnce(t *testing.T) {
	old := ticketFor("OLD", time.Now().Add(20*24*time.Hour))
	srv, drive := newFakeDrive(t)
	defer srv.Close()

	c, err := New(old)
	if err != nil {
		t.Fatal(err)
	}
	c.SetRefreshURL(srv.URL + "/refresh")
	c.SetRouteBase(srv.URL) // keep the route lookup off the public internet
	c.host = srv.URL

	var wg sync.WaitGroup
	errs := make([]error, 12)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = echoPost(t, c)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			d := drive
			t.Fatalf("request %d failed: %v | refresh=%d rejected=%v current=%q new=%q",
				i, err, d.refreshN.Load(), d.rejected, c.Authorization(), d.newToken)
		}
	}
	if n := drive.refreshN.Load(); n != 1 {
		t.Fatalf("refresh calls=%d want 1", n)
	}
}

// TestForcedRefreshFailureBacksOff makes sure a credential that cannot be
// renewed does not turn every request into a call to the renewal endpoint.
func TestForcedRefreshFailureBacksOff(t *testing.T) {
	old := ticketFor("OLD", time.Now().Add(20*24*time.Hour))
	srv, drive := newFakeDrive(t)
	defer srv.Close()
	drive.refreshFail = http.StatusInternalServerError

	c, err := New(old)
	if err != nil {
		t.Fatal(err)
	}
	c.SetRefreshURL(srv.URL + "/refresh")
	c.SetRouteBase(srv.URL) // keep the route lookup off the public internet
	c.host = srv.URL

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = echoPost(t, c)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err == nil {
			t.Fatalf("request %d unexpectedly succeeded", i)
		}
		if !strings.Contains(err.Error(), "登录状态已失效") {
			t.Fatalf("request %d: %v (the service error should surface)", i, err)
		}
	}
	// One recovery tries both ticket shapes, so two calls to the endpoint mean
	// "one renewal attempt" — not that the endpoint was hammered.
	if n := drive.refreshN.Load(); n != 2 {
		t.Fatalf("refresh calls=%d want 2 (a single recovery)", n)
	}
	if st := c.AuthState(); !strings.Contains(st.LastError, "自动续期失败") {
		t.Fatalf("LastError=%q should explain the failed renewal", st.LastError)
	}
	if got := c.Authorization(); got != old {
		t.Fatalf("credential changed on a failed refresh: %q", got)
	}
}

// TestNewCredentialClearsRefreshBackoff: pasting a fresh ticket must take effect
// immediately, without waiting out the back-off window.
func TestNewCredentialClearsRefreshBackoff(t *testing.T) {
	old := ticketFor("OLD", time.Now().Add(20*24*time.Hour))
	srv, drive := newFakeDrive(t)
	defer srv.Close()
	drive.refreshFail = http.StatusInternalServerError

	c, err := New(old)
	if err != nil {
		t.Fatal(err)
	}
	c.SetRefreshURL(srv.URL + "/refresh")
	c.SetRouteBase(srv.URL) // keep the route lookup off the public internet
	c.host = srv.URL

	if _, err := echoPost(t, c); err == nil {
		t.Fatal("expected the first request to fail")
	}
	before := drive.refreshN.Load()
	drive.mu.Lock()
	drive.refreshFail = 0
	drive.mu.Unlock()

	// The user pastes a good ticket: the back-off must not swallow it.
	if err := c.SetAuthorization(old); err != nil {
		t.Fatal(err)
	}
	c.host = srv.URL
	echo, err := echoPost(t, c)
	if err != nil {
		t.Fatalf("post after new credential: %v", err)
	}
	if echo != "hi" {
		t.Fatalf("echo=%q", echo)
	}
	if n := drive.refreshN.Load() - before; n < 1 || n > 2 {
		t.Fatalf("refresh calls after the new credential=%d want 1 renewal", n)
	}
}

// TestAuthFailureClassification pins the rule that decides whether a failed
// call is worth a refresh.
func TestAuthFailureClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"http 401", &APIError{Status: http.StatusUnauthorized, Message: "移动云盘返回 HTTP 401"}, true},
		{"http 403", &APIError{Status: http.StatusForbidden}, true},
		{"code 12002", &APIError{Code: "12002", Message: "token 已失效"}, true},
		{"message 登录", &APIError{Code: "9", Message: "登录状态已失效"}, true},
		{"no auth", ErrNoAuth, true},
		{"business code", &APIError{Code: "200000408", Message: "同名文件已存在"}, false},
		{"not found", &APIError{Code: "200000404", Message: "文件不存在"}, false},
		{"plain", fmt.Errorf("移动云盘返回 HTTP 500"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		if got := authFailure(tc.err); got != tc.want {
			t.Errorf("%s: authFailure=%v want %v", tc.name, got, tc.want)
		}
	}
}

// TestUploadResignsRejectedPartURL keeps a long upload alive when the service
// refuses a pre-signed part URL mid-transfer.
func TestUploadResignsRejectedPartURL(t *testing.T) {
	const partSize = 64 << 10
	auth := ticketFor("T", time.Now().Add(30*24*time.Hour))

	var srv *httptest.Server
	var signCalls, partPuts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/file/create", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Size      int64      `json:"size"`
			PartInfos []partInfo `json:"partInfos"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		parts := []map[string]any{}
		for _, p := range body.PartInfos {
			if p.PartNumber > 1 { // the tail is signed on demand
				continue
			}
			parts = append(parts, map[string]any{
				"partNumber": p.PartNumber,
				"partSize":   p.PartSize,
				"uploadUrl":  fmt.Sprintf("%s/part/%d", srv.URL, p.PartNumber),
			})
		}
		writeJSON(w, map[string]any{"success": true, "code": "0", "message": "成功",
			"data": map[string]any{
				"fileId": "F1", "fileName": "big.bin", "uploadId": "U1", "partInfos": parts,
			}})
	})
	mux.HandleFunc("/file/getUploadUrl", func(w http.ResponseWriter, r *http.Request) {
		signCalls.Add(1)
		var body struct {
			PartInfos []partInfo `json:"partInfos"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		// The first signing round hands out links that the drive will refuse
		// (as if the signature expired); the second round is accepted.
		suffix := "?v=2"
		if signCalls.Load() == 1 {
			suffix = ""
		}
		parts := []map[string]any{}
		for _, p := range body.PartInfos {
			parts = append(parts, map[string]any{
				"partNumber": p.PartNumber,
				"partSize":   p.PartSize,
				"uploadUrl":  fmt.Sprintf("%s/part/%d%s", srv.URL, p.PartNumber, suffix),
			})
		}
		writeJSON(w, map[string]any{"success": true, "code": "0",
			"data": map[string]any{"partInfos": parts}})
	})
	mux.HandleFunc("/file/complete", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"success": true, "code": "0", "message": "成功"})
	})
	mux.HandleFunc("/part/", func(w http.ResponseWriter, r *http.Request) {
		partPuts.Add(1)
		io.Copy(io.Discard, r.Body)
		n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/part/"))
		// The first URL handed out for part 2 is refused, as if it expired.
		if n == 2 && r.URL.Query().Get("v") != "2" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	c, err := New(auth)
	if err != nil {
		t.Fatal(err)
	}
	c.host = srv.URL
	c.SetRefreshURL(srv.URL + "/refresh")

	size := int64(3 * partSize)
	src := bytes.NewReader(make([]byte, size))
	name, err := c.UploadReader(context.Background(), "/", "big.bin", src, size,
		UploadOptions{PartSize: partSize})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if name != "big.bin" {
		t.Fatalf("stored name=%q", name)
	}
	if n := signCalls.Load(); n != 2 {
		t.Fatalf("sign calls=%d want 2 (initial tail + re-sign)", n)
	}
	if n := partPuts.Load(); n != 4 {
		t.Fatalf("part PUTs=%d want 4 (1,2 refused,2 again,3)", n)
	}
}

// A part URL can also go stale without any auth problem (expired signature, the
// upload session moved). That must recover too — 404/410 are not "auth failures".
func TestUploadResignsOnExpiredPartURL(t *testing.T) {
	const partSize = 64 << 10
	auth := ticketFor("T", time.Now().Add(30*24*time.Hour))

	var srv *httptest.Server
	var signCalls, partPuts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/file/create", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			PartInfos []partInfo `json:"partInfos"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		parts := []map[string]any{}
		for _, p := range body.PartInfos {
			if p.PartNumber > 1 {
				continue
			}
			parts = append(parts, map[string]any{
				"partNumber": p.PartNumber, "partSize": p.PartSize,
				"uploadUrl": fmt.Sprintf("%s/part/%d", srv.URL, p.PartNumber),
			})
		}
		writeJSON(w, map[string]any{"success": true, "code": "0", "message": "成功",
			"data": map[string]any{"fileId": "F1", "fileName": "big.bin",
				"uploadId": "U1", "partInfos": parts}})
	})
	mux.HandleFunc("/file/getUploadUrl", func(w http.ResponseWriter, r *http.Request) {
		signCalls.Add(1)
		var body struct {
			PartInfos []partInfo `json:"partInfos"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		// First round hands out an already-expired link; the re-sign is good.
		suffix := "?v=2"
		if signCalls.Load() == 1 {
			suffix = ""
		}
		parts := []map[string]any{}
		for _, p := range body.PartInfos {
			parts = append(parts, map[string]any{
				"partNumber": p.PartNumber, "partSize": p.PartSize,
				"uploadUrl": fmt.Sprintf("%s/part/%d%s", srv.URL, p.PartNumber, suffix),
			})
		}
		writeJSON(w, map[string]any{"success": true, "code": "0",
			"data": map[string]any{"partInfos": parts}})
	})
	mux.HandleFunc("/file/complete", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"success": true, "code": "0", "message": "成功"})
	})
	mux.HandleFunc("/part/", func(w http.ResponseWriter, r *http.Request) {
		partPuts.Add(1)
		io.Copy(io.Discard, r.Body)
		n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/part/"))
		if n == 2 && r.URL.Query().Get("v") != "2" {
			w.WriteHeader(http.StatusNotFound) // signature expired → 404, not 401/403
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	c, err := New(auth)
	if err != nil {
		t.Fatal(err)
	}
	c.host = srv.URL
	c.SetRefreshURL(srv.URL + "/refresh") // must NOT be needed here

	size := int64(3 * partSize)
	src := bytes.NewReader(make([]byte, size))
	if _, err := c.UploadReader(context.Background(), "/", "big.bin", src, size,
		UploadOptions{PartSize: partSize}); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if n := signCalls.Load(); n != 2 {
		t.Fatalf("sign calls=%d want 2 (initial tail + re-sign)", n)
	}
	if n := partPuts.Load(); n != 4 {
		t.Fatalf("part PUTs=%d want 4", n)
	}
}
