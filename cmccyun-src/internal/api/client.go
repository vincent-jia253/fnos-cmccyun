// Package api implements the China Mobile Cloud Drive (中国移动云盘 / 139 网盘)
// client used by the fnOS mount daemon.
//
// Two very different APIs exist for this service:
//
//  1. The official open platform (https://open.yun.139.com, host
//     miniapp.yun.139.com). It requires a signed cooperation contract with
//     中移互联网, and it deliberately restricts writes to the
//     "我的应用收藏/<app>" folder, so it cannot back a full-disk mount.
//
//  2. The first-party web API used by https://yun.139.com itself. This is what
//     every working mount implementation on the market uses, and it is what
//     this package implements.
//
// See research/cmcc-cloud-api.md in the project workspace for the full
// comparison and for the source of every endpoint below.
package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	routeHost = "https://user-njs.yun.139.com"
	// clientInfoType is the 3rd field of X-Yun-Client-Info. The service uses it
	// to pick the per-file upload ceiling: 9 (web) / 2 (app) = 50 GB,
	// 13 (PC client) = 500 GB.
	clientInfoType = "13"

	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

// ErrNoAuth is returned when the client has no usable credential.
var ErrNoAuth = errors.New("未配置移动云盘授权信息")

// Client talks to the 139 web API on behalf of one account.
type Client struct {
	http *http.Client

	mu            sync.RWMutex
	authorization string // base64("pc:<account>:<token>")
	account       string
	host          string // resolved personal-cloud endpoint
	expiryMillis  int64  // token expiry, unix milliseconds
	lastRefresh   time.Time
	lastErr       string
	clientType    string // 3rd field of X-Yun-Client-Info
	refreshFunc   func(string)
	refreshURL    string // override for the AAS refresh endpoint (tests)

	routeBase   string       // override for the route lookup (empty = default)
	recoverFunc func(string) // operator-visible notice when a renewal kicks in
	refreshMu   sync.Mutex   // serialises on-demand credential recovery
	lastForced  time.Time    // when the credential was last renewed on demand
	forcedOK    bool         // did the last on-demand renewal succeed?
	forcedErr   string       // why the last on-demand renewal failed
}

// Cooldowns that keep a broken credential from turning every request into a
// call to the renewal endpoint.
const (
	forcedSuccessCooldown = 30 * time.Second
	forcedFailureCooldown = 2 * time.Minute
)

// defaultRefreshURL is the AAS endpoint the browser client itself uses to renew
// a 139 token.
const defaultRefreshURL = "https://aas.caiyun.feixin.10086.cn:443/tellin/authTokenRefresh.do"

// New creates a client from the raw `Authorization` header value captured from
// the yun.139.com web client (the part after "Basic ").
func New(authorization string) (*Client, error) {
	c := &Client{
		http: &http.Client{
			Timeout: 0, // downloads/uploads are long lived; contexts govern them
			Transport: &http.Transport{
				MaxIdleConns:        32,
				MaxIdleConnsPerHost: 16,
				IdleConnTimeout:     90 * time.Second,
				Proxy:               http.ProxyFromEnvironment,
			},
		},
	}
	c.clientType = clientInfoType
	if err := c.SetAuthorization(authorization); err != nil {
		return nil, err
	}
	return c, nil
}

// SetClientType overrides the client type advertised in X-Yun-Client-Info.
// The service caps a single upload according to it: 9 (web) / 2 (app) = 50 GB,
// 13 (PC client) = 500 GB.
func (c *Client) SetClientType(t string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t == "" {
		t = clientInfoType
	}
	c.clientType = t
}

func (c *Client) clientInfoType() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.clientType == "" {
		return clientInfoType
	}
	return c.clientType
}

// SetAuthorization (re)loads the credential.
func (c *Client) SetAuthorization(authorization string) error {
	authorization = strings.TrimSpace(authorization)
	authorization = strings.TrimPrefix(authorization, "Basic ")
	authorization = strings.TrimPrefix(authorization, "basic ")
	if authorization == "" {
		c.mu.Lock()
		c.authorization, c.account, c.host, c.expiryMillis = "", "", "", 0
		c.mu.Unlock()
		return ErrNoAuth
	}
	defer c.resetForcedState()
	raw, err := base64.StdEncoding.DecodeString(authorization)
	if err != nil {
		// Tolerate a non-base64 blob: some users paste the decoded triplet.
		raw = []byte(authorization)
	}
	parts := strings.Split(string(raw), ":")
	if len(parts) < 3 {
		return errors.New("Authorization 格式不正确，应为 base64(\"pc:账号:令牌\")")
	}
	var expiry int64
	if segs := strings.Split(parts[2], "|"); len(segs) >= 4 {
		if v, err := strconv.ParseInt(segs[3], 10, 64); err == nil {
			expiry = v
		}
	}
	c.mu.Lock()
	c.authorization = base64.StdEncoding.EncodeToString([]byte(strings.Join(parts[:3], ":")))
	c.account = parts[1]
	c.expiryMillis = expiry
	c.host = ""
	c.mu.Unlock()
	return nil
}

// resetForcedState lets the next request try a renewal from scratch; it is
// called whenever a (new) credential arrives.
func (c *Client) resetForcedState() {
	c.mu.Lock()
	c.lastForced, c.forcedOK, c.forcedErr = time.Time{}, false, ""
	c.mu.Unlock()
}

// Account returns the logged-in mobile number.
func (c *Client) Account() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.account
}

// Ready reports whether a credential is present.
func (c *Client) Ready() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.authorization != ""
}

// Init resolves the per-account personal-cloud host and refreshes the token
// when it is close to expiry.
func (c *Client) Init(ctx context.Context) error {
	if !c.Ready() {
		return ErrNoAuth
	}
	if err := c.EnsureFresh(ctx); err != nil {
		return err
	}
	return c.resolveHost(ctx)
}

// AuthState is the credential health shown in the console.
type AuthState struct {
	Account     string        `json:"account"`
	HasToken    bool          `json:"has_token"`
	ExpiresAt   *time.Time    `json:"expires_at,omitempty"`
	ExpiresIn   time.Duration `json:"-"`
	ExpiresDays float64       `json:"expires_days"`
	LastRefresh *time.Time    `json:"last_refresh,omitempty"`
	LastError   string        `json:"last_error,omitempty"`
	Configured  bool          `json:"configured"`
}

// AuthState snapshots the credential health.
func (c *Client) AuthState() AuthState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	st := AuthState{
		Account:    c.account,
		HasToken:   c.authorization != "",
		Configured: c.authorization != "",
		LastError:  c.lastErr,
	}
	if !c.lastRefresh.IsZero() {
		t := c.lastRefresh
		st.LastRefresh = &t
	}
	if c.expiryMillis > 0 {
		t := time.UnixMilli(c.expiryMillis)
		st.ExpiresAt = &t
		st.ExpiresIn = time.Until(t)
		st.ExpiresDays = st.ExpiresIn.Hours() / 24
	}
	return st
}

// SetRefreshFunc registers a callback invoked with the new Authorization value
// after every successful refresh, so the caller can persist it.
func (c *Client) SetRefreshFunc(fn func(string)) {
	c.mu.Lock()
	c.refreshFunc = fn
	c.mu.Unlock()
}

// Authorization returns the current (possibly refreshed) Authorization value.
func (c *Client) Authorization() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.authorization
}

func (c *Client) setAuthError(msg string) {
	c.mu.Lock()
	c.lastErr = msg
	c.mu.Unlock()
}

// EnsureFresh refreshes the token when it is close to expiry and reports the
// outcome. A token with more than 15 days left is left alone, mirroring the
// OpenList 139 driver (whose threshold and refresh endpoint this follows).
func (c *Client) EnsureFresh(ctx context.Context) error {
	c.mu.RLock()
	expiry := c.expiryMillis
	auth := c.authorization
	c.mu.RUnlock()
	if auth == "" {
		return ErrNoAuth
	}
	if expiry == 0 {
		return nil // token shape unknown: nothing we can schedule
	}
	remaining := time.Until(time.UnixMilli(expiry))
	if remaining > 15*24*time.Hour {
		return nil
	}
	if remaining < 0 {
		err := errors.New("移动云盘令牌已过期，请重新获取 Authorization 并粘贴到设置中")
		c.setAuthError(err.Error())
		return err
	}
	if err := c.RefreshToken(ctx); err != nil {
		c.setAuthError("刷新令牌失败: " + err.Error())
		return err
	}
	c.setAuthError("")
	return nil
}

type refreshResp struct {
	XMLName xml.Name `xml:"root"`
	Return  string   `xml:"return"`
	Desc    string   `xml:"desc"`
	Token   string   `xml:"token"`
}

// RefreshToken renews the access token through the AAS endpoint.
//
// The ticket captured from the browser is base64("pc:<account>:<token>") and
// the token field itself carries extra "|"-separated metadata (the 4th field is
// its expiry in unix milliseconds). The refresh endpoint accepts the whole
// ticket blob in some deployments and only the leading token in others, so both
// shapes are tried (the OpenList 139 driver posts the whole blob).
func (c *Client) RefreshToken(ctx context.Context) error {
	c.mu.RLock()
	auth := c.authorization
	c.mu.RUnlock()
	raw, err := base64.StdEncoding.DecodeString(auth)
	if err != nil {
		return err
	}
	parts := strings.SplitN(string(raw), ":", 3)
	if len(parts) < 3 {
		return errors.New("Authorization 格式不正确")
	}
	account := parts[1]
	segs := strings.Split(parts[2], "|")

	candidates := []string{parts[2]}
	if len(segs) > 1 {
		candidates = append(candidates, segs[0])
	}

	var lastErr error
	for _, ticket := range candidates {
		token, err := c.postRefresh(ctx, ticket, account)
		if err != nil {
			lastErr = err
			continue
		}
		newAuth := base64.StdEncoding.EncodeToString(
			[]byte(parts[0] + ":" + account + ":" + token))
		var expiry int64
		if ns := strings.Split(token, "|"); len(ns) >= 4 {
			if v, err := strconv.ParseInt(ns[3], 10, 64); err == nil {
				expiry = v
			}
		}
		c.mu.Lock()
		c.authorization = newAuth
		c.lastRefresh = time.Now()
		c.lastErr = ""
		if expiry > 0 {
			c.expiryMillis = expiry
		}
		fn := c.refreshFunc
		c.mu.Unlock()
		if fn != nil {
			fn(newAuth)
		}
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("刷新令牌被拒绝")
	}
	return lastErr
}

// authFailure reports whether an error means the service rejected our
// credential (rather than the request itself being invalid).
func authFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNoAuth) {
		return true
	}
	var ae *APIError
	if errors.As(err, &ae) {
		if ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden {
			return true
		}
		switch ae.Code {
		case "401", "403", "10401", "12002":
			return true
		}
		for _, s := range []string{
			"登录", "授权", "鉴权", "凭证", "token", "Token", "expired", "unauthorized",
		} {
			if strings.Contains(ae.Message, s) {
				return true
			}
		}
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "HTTP 401") || strings.Contains(msg, "HTTP 403")
}

// staleStatus reports whether an HTTP status means "this pre-signed address is
// no longer usable" rather than "this request is wrong". A CDN or part endpoint
// answers 400/409/410/404 once its signature expired or the upload session moved.
func staleStatus(code int) bool {
	switch code {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
		http.StatusNotFound, http.StatusConflict, http.StatusGone:
		return true
	}
	return false
}

// staleURL is staleStatus for errors that carry an HTTP status.
func staleURL(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && staleStatus(ae.Status)
}

// forceRefreshOnce renews the credential after the service rejected it.
//
// failed is the Authorization value that was refused. If the credential has
// already changed in the meantime another request recovered it first and there
// is nothing to do — that also makes the recovery safe for the many concurrent
// requests a busy mount produces: exactly one of them talks to the refresh
// endpoint and the rest just retry with the new ticket.
func (c *Client) forceRefreshOnce(ctx context.Context, failed string) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	c.mu.RLock()
	cur, lastForced, forcedOK, forcedErr := c.authorization, c.lastForced, c.forcedOK, c.forcedErr
	c.mu.RUnlock()
	if cur != failed {
		return nil
	}
	cooldown := forcedSuccessCooldown
	if !forcedOK {
		cooldown = forcedFailureCooldown
	}
	if !lastForced.IsZero() && time.Since(lastForced) < cooldown {
		// The endpoint was just asked. Retrying immediately would only add load,
		// so report the outcome of that attempt instead.
		if forcedErr != "" {
			return errors.New(forcedErr)
		}
		return errors.New("授权刚刚续期过，仍被拒绝")
	}
	err := c.RefreshToken(ctx)
	c.mu.Lock()
	c.lastForced, c.forcedOK = time.Now(), err == nil
	if err != nil {
		c.forcedErr = err.Error()
	} else {
		c.forcedErr = ""
	}
	c.mu.Unlock()
	if err != nil {
		return err
	}
	c.setAuthError("")
	return nil
}

// SetRefreshURL points the refresh call at another endpoint. Used by the tests
// (and by anyone who needs to go through a mirror).
// SetRecoverFunc registers a callback that reports on-demand credential
// renewals, so the console log shows why a request suddenly recovered.
func (c *Client) SetRecoverFunc(f func(string)) {
	c.mu.Lock()
	c.recoverFunc = f
	c.mu.Unlock()
}

// noteRecovery reports a renewal without holding any lock while the callback
// runs (the callback writes to the service log).
func (c *Client) noteRecovery(format string, args ...any) {
	c.mu.RLock()
	f := c.recoverFunc
	c.mu.RUnlock()
	if f != nil {
		f(fmt.Sprintf(format, args...))
	}
}

// shortURL keeps log lines readable: host + path is more than an operator needs.
func shortURL(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		if j := strings.Index(u[i+3:], "/"); j >= 0 {
			return u[i+3+j:]
		}
	}
	return u
}

// SetRouteBase points the account-route lookup at another endpoint. Used by the
// tests (and by anyone who needs to go through a mirror).
func (c *Client) SetRouteBase(u string) {
	c.mu.Lock()
	c.routeBase = strings.TrimRight(u, "/")
	c.mu.Unlock()
}

func (c *Client) SetRefreshURL(u string) {
	c.mu.Lock()
	c.refreshURL = u
	c.mu.Unlock()
}

// postRefresh performs one refresh attempt against the AAS endpoint.
func (c *Client) postRefresh(ctx context.Context, ticket, account string) (string, error) {
	body := "<root><token>" + ticket + "</token><account>" + account +
		"</account><clienttype>656</clienttype></root>"
	url := defaultRefreshURL
	if c.refreshURL != "" {
		url = c.refreshURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		url, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/xml")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return "", &APIError{Status: resp.StatusCode,
			Message: fmt.Sprintf("刷新令牌失败: 移动云盘返回 HTTP %d", resp.StatusCode)}
	}
	var rr refreshResp
	if err := xml.Unmarshal(data, &rr); err != nil {
		return "", fmt.Errorf("刷新令牌失败: %w", err)
	}
	if rr.Return != "0" || rr.Token == "" {
		desc := rr.Desc
		if desc == "" {
			desc = "令牌刷新被拒绝"
		}
		return "", errors.New(desc)
	}
	return rr.Token, nil
}

// resolveHost asks the route service which regional personal-cloud endpoint
// this account must talk to.
func (c *Client) resolveHost(ctx context.Context) error {
	c.mu.RLock()
	host := c.host
	account := c.account
	c.mu.RUnlock()
	if host != "" || account == "" {
		return nil
	}
	body := map[string]any{
		"userInfo": map[string]any{
			"userType":    1,
			"accountType": 1,
			"accountName": account,
		},
		"modAddrType": 1,
	}
	var out routePolicyResp
	base := c.routeBase
	if base == "" {
		base = routeHost
	}
	if err := c.do(ctx, base+"/user/route/qryRoutePolicy", body, &out, false); err != nil {
		return err
	}
	for _, p := range out.Data.RoutePolicyList {
		if p.ModName == "personal" && p.HttpsUrl != "" {
			c.mu.Lock()
			c.host = strings.TrimRight(p.HttpsUrl, "/")
			c.mu.Unlock()
			return nil
		}
	}
	if out.Data.RoutePolicyList == nil {
		return fmt.Errorf("路由查询失败: %s", out.Message)
	}
	return errors.New("未能解析个人云服务地址")
}

// RefreshHost forces a re-resolution of the personal-cloud host.
func (c *Client) RefreshHost(ctx context.Context) error {
	c.mu.Lock()
	c.host = ""
	c.mu.Unlock()
	return c.resolveHost(ctx)
}

func randomString(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		b[i] = alphabet[idx.Int64()]
	}
	return string(b)
}

// do performs one signed POST, renewing the credential and retrying once when
// the service rejects it. `personal` selects between the route service headers
// (yun.139.com style) and the personal-cloud headers.
//
// A mount stays up for months while a ticket only lives about 30 days, so a
// credential can be invalidated mid-flight (the user logs in somewhere else, the
// service rotates the session). Refreshing on the spot — instead of waiting for
// the six-hourly sweep — is what keeps such a mount alive.
func (c *Client) do(ctx context.Context, url string, payload any, out any, personal bool) error {
	auth := c.Authorization()
	err := c.doOnce(ctx, url, auth, payload, out, personal)
	if err == nil || !authFailure(err) {
		return err
	}
	// Recover against the credential this attempt actually used: by now another
	// request may already have replaced it, in which case there is nothing to do.
	c.noteRecovery("%s 鉴权失败，立刻强制续期后重试", shortURL(url))
	if rerr := c.forceRefreshOnce(ctx, auth); rerr != nil {
		c.setAuthError("授权已失效且自动续期失败: " + rerr.Error())
		c.noteRecovery("续期未成功（%v），%s 重试取消", rerr, shortURL(url))
		return err // the caller cares about the service's error, not our retry
	}
	if ctx.Err() != nil {
		return err
	}
	err2 := c.doOnce(ctx, url, c.Authorization(), payload, out, personal)
	if err2 != nil && authFailure(err2) {
		c.setAuthError("续期后的授权仍被拒绝: " + err2.Error())
		c.noteRecovery("续期后 %s 仍被拒绝", shortURL(url))
		return err2
	}
	if err2 == nil && c.Authorization() != auth {
		c.noteRecovery("续期成功，%s 已用新令牌重试通过", shortURL(url))
	}
	return err2
}

// doOnce performs a single signed POST with the given credential and without
// any recovery.
func (c *Client) doOnce(ctx context.Context, url, auth string, payload any, out any, personal bool) error {

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ts := time.Now().Format("2006-01-02 15:04:05")
	rnd := randomString(16)
	sign := calSign(string(body), ts, rnd)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("mcloud-sign", ts+","+rnd+","+sign)
	req.Header.Set("mcloud-channel", "1000101")
	req.Header.Set("mcloud-client", "10701")
	req.Header.Set("mcloud-version", "7.14.0")
	req.Header.Set("x-DeviceInfo", "||9|7.14.0|chrome|120.0.0.0|||windows 10||zh-CN|||")
	req.Header.Set("x-huawei-channelSrc", "10000034")
	req.Header.Set("x-inner-ntwk", "2")
	req.Header.Set("x-m4c-caller", "PC")
	req.Header.Set("x-m4c-src", "10002")
	req.Header.Set("x-SvcType", "1")

	if personal {
		req.Header.Set("Caller", "web")
		req.Header.Set("Cms-Device", "default")
		req.Header.Set("Mcloud-Route", "001")
		req.Header.Set("X-Yun-Api-Version", "v1")
		req.Header.Set("X-Yun-App-Channel", "10000034")
		req.Header.Set("X-Yun-Channel-Source", "10000034")
		req.Header.Set("X-Yun-Module-Type", "100")
		req.Header.Set("X-Yun-Svc-Type", "1")
		req.Header.Set("X-Yun-Client-Info",
			"||"+c.clientInfoType()+"|7.14.0|chrome|120.0.0.0|||windows 10||zh-CN|||dW5kZWZpbmVk||")
		req.Header.Set("Origin", "https://yun.139.com")
		req.Header.Set("Referer", "https://yun.139.com/")
	} else {
		req.Header.Set("CMS-DEVICE", "default")
		req.Header.Set("Inner-Hcy-Router-Https", "1")
		req.Header.Set("Origin", "https://yun.139.com")
		req.Header.Set("Referer", "https://yun.139.com/w/")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		// 401/403 usually mean the ticket was invalidated mid-session; the body
		// often still carries the service's own code/message.
		var env baseResp
		_ = json.Unmarshal(data, &env)
		msg := env.Message
		if msg == "" {
			msg = fmt.Sprintf("移动云盘返回 HTTP %d", resp.StatusCode)
		}
		return &APIError{Code: env.Code, Message: msg, Status: resp.StatusCode}
	}

	var env baseResp
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}
	if !env.Success {
		msg := env.Message
		if msg == "" {
			msg = env.Code
		}
		return &APIError{Code: env.Code, Message: msg}
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// post sends a request to the personal-cloud host, resolving it on first use.
func (c *Client) post(ctx context.Context, path string, payload any, out any) error {
	c.mu.RLock()
	host := c.host
	c.mu.RUnlock()
	if host == "" {
		if err := c.resolveHost(ctx); err != nil {
			return err
		}
		c.mu.RLock()
		host = c.host
		c.mu.RUnlock()
	}
	if host == "" {
		return errors.New("个人云服务地址为空")
	}
	err := c.do(ctx, host+path, payload, out, true)
	if err != nil && authFailure(err) {
		// The credential may belong to a different regional endpoint: drop the
		// cached host and resolve it once more before giving up.
		c.mu.Lock()
		c.host = ""
		c.mu.Unlock()
		if herr := c.resolveHost(ctx); herr == nil {
			c.mu.RLock()
			host = c.host
			c.mu.RUnlock()
			derr := c.do(ctx, host+path, payload, out, true)
			if derr == nil {
				return nil
			}
			err = derr
		}
	}
	return err
}

// APIError is a business-level error returned by the service. Status is the
// HTTP status code when the request did not return 200 (0 otherwise).
type APIError struct {
	Code    string
	Message string
	Status  int
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return "移动云盘接口错误: " + e.Code
	}
	return e.Message
}

// ---------------------------------------------------------------- operations

// RootID is the identifier of the personal-cloud root folder.
const RootID = "/"

// List returns one directory page. fileId "/" means the root folder.
func (c *Client) List(ctx context.Context, fileId string) ([]File, error) {
	if fileId == "" {
		fileId = RootID
	}
	var out []File
	cursor := ""
	for {
		payload := map[string]any{
			"imageThumbnailStyleList": []string{"Small", "Large"},
			"orderBy":                 "updated_at",
			"orderDirection":          "DESC",
			"pageInfo": map[string]any{
				"pageCursor": cursor,
				"pageSize":   100,
			},
			"parentFileId": fileId,
		}
		var resp listResp
		if err := c.post(ctx, "/file/list", payload, &resp); err != nil {
			return nil, err
		}
		for _, it := range resp.Data.Items {
			out = append(out, File{
				ID:       it.FileId,
				Name:     it.Name,
				Size:     it.Size,
				IsDir:    it.Type == "folder",
				Created:  it.CreatedAt,
				Modified: it.UpdatedAt,
			})
		}
		cursor = resp.Data.NextPageCursor
		if cursor == "" {
			return out, nil
		}
	}
}

// DownloadURL returns a fresh, signed CDN link for a file.
func (c *Client) DownloadURL(ctx context.Context, fileId string) (string, error) {
	var resp downloadResp
	if err := c.post(ctx, "/file/getDownloadUrl",
		map[string]any{"fileId": fileId}, &resp); err != nil {
		return "", err
	}
	if resp.Data.CdnUrl != "" {
		return resp.Data.CdnUrl, nil
	}
	if resp.Data.Url == "" {
		return "", errors.New("下载地址为空")
	}
	return resp.Data.Url, nil
}

// Mkdir creates a folder. The service may rename it on conflict, so the
// effective name is returned.
func (c *Client) Mkdir(ctx context.Context, parentID, name string) (string, error) {
	payload := map[string]any{
		"parentFileId":   parentID,
		"name":           name,
		"description":    "",
		"type":           "folder",
		"fileRenameMode": "force_rename",
	}
	var resp createResp
	if err := c.post(ctx, "/file/create", payload, &resp); err != nil {
		return "", err
	}
	if resp.Data.FileName != "" {
		return resp.Data.FileName, nil
	}
	return name, nil
}

// Rename renames a file or folder.
func (c *Client) Rename(ctx context.Context, fileID, name string) error {
	payload := map[string]any{
		"fileId":      fileID,
		"name":        name,
		"description": "",
	}
	return c.post(ctx, "/file/update", payload, nil)
}

// Move moves entries into another folder.
func (c *Client) Move(ctx context.Context, ids []string, toParent string) error {
	payload := map[string]any{"fileIds": ids, "toParentFileId": toParent}
	return c.post(ctx, "/file/batchMove", payload, nil)
}

// Copy copies entries into another folder.
func (c *Client) Copy(ctx context.Context, ids []string, toParent string) error {
	payload := map[string]any{"fileIds": ids, "toParentFileId": toParent}
	return c.post(ctx, "/file/batchCopy", payload, nil)
}

// Trash moves entries into the recycle bin.
func (c *Client) Trash(ctx context.Context, ids []string) error {
	payload := map[string]any{"fileIds": ids}
	return c.post(ctx, "/recyclebin/batchTrash", payload, nil)
}

// Download fetches a byte range of a file. The signed CDN link is short lived,
// so a 401/403/410 re-issues it once (which also renews an expired ticket)
// before the read is declared dead.
func (c *Client) Download(ctx context.Context, fileID string, offset, length int64) (io.ReadCloser, error) {
	url, err := c.DownloadURL(ctx, fileID)
	if err != nil {
		return nil, err
	}
	body, status, err := c.fetchRange(ctx, url, offset, length)
	if err == nil {
		return body, nil
	}
	if !staleStatus(status) {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, err
	}
	url, uerr := c.DownloadURL(ctx, fileID)
	if uerr != nil {
		return nil, err
	}
	c.noteRecovery("下载直链被拒，已重新获取直链后重试")
	body, _, err = c.fetchRange(ctx, url, offset, length)
	return body, err
}

// fetchRange performs one ranged GET against a signed link.
func (c *Client) fetchRange(ctx context.Context, url string, offset, length int64) (io.ReadCloser, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	if length > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))
	} else if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		return nil, resp.StatusCode, fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}
	return resp.Body, resp.StatusCode, nil
}
