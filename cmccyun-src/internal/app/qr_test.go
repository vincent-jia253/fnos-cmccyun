package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 用户明确要的两个入口：扫码登录（新，学竞品）和手工粘贴（旧，保留）。
// 两个都得在页面上找得到，而且要说清各自适合什么场景 —— 少一个就是回归。
func TestAuthTabOffersBothLoginWays(t *testing.T) {
	for _, want := range []string{
		// 扫码登录
		`id="qrImg"`,
		`id="btnQrStart"`,
		`id="qrMsg"`,
		"/api/auth/qr/start",
		"/api/auth/qr/poll",
		"扫码登录",
		"确认登录",
		// 手工粘贴（v1.3.x 的老路，不能删）
		`id="f_auth"`,
		`id="btnSaveAuth"`,
		`id="btnRefreshAuth"`,
		"手工粘贴",
		// 两种方式的选择说明
		"两种登录方式怎么选",
		"base64(\"pc:账号:令牌\")",
		"随便挑一个",
		// 开发者署名
		"开发者 <b>vincent</b>",
		`id="footVer"`,
		// 图标（浏览器标签页）
		`rel="icon"`,
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("界面缺少 %q（扫码 + 手工粘贴两种登录方式都要在，且带说明）", want)
		}
	}
}

// 二维码必须由服务端渲染成 PNG 内嵌，前端不许依赖外网 CDN —— 飞牛盒子经常
// 没有外网，靠 CDN 的 QR 库会直接白屏。
func TestQrRenderingIsSelfContained(t *testing.T) {
	for _, bad := range []string{"cdn.jsdelivr", "unpkg.com", "cdnjs.", "qrcode.min.js"} {
		if strings.Contains(indexHTML, bad) {
			t.Errorf("界面引用了外网资源 %q：控制台必须离线可用", bad)
		}
	}
	// 图片来自 /api/auth/qr/start 返回的 data URI（后端拼 base64），不是前端库画的
	if !strings.Contains(indexHTML, `$("qrImg").src = d.image;`) {
		t.Error("页面没有把接口返回的二维码图（data URI）贴到 <img> 上")
	}
}

// 路由必须在，而且：start 只收 POST；poll 传个不存在的 id 要立刻回 expired，
// 绝对不能去连云端（测试环境没网，也不该等 30 秒超时）。
func TestQrEndpointsRegistered(t *testing.T) {
	s, _ := newMountSvc(t, "")
	h := s.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/qr/start", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("GET /api/auth/qr/start 应该 400（只收 POST），实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "POST") {
		t.Errorf("提示里应该说清要用 POST，实际：%s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/auth/qr/poll?id=不存在的会话", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/auth/qr/poll 未知 id 应该 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"expired"`) {
		t.Errorf("未知会话应回 status=expired，实际：%s", body)
	}
	if !strings.Contains(body, `"ok":true`) {
		t.Errorf("未知会话不算错误，ok 应为 true，实际：%s", body)
	}
}

// 会话表不能无限增长：过期的要顺手清掉，成功/失败/过期三种收尾都要删。
func TestQrSessionTableDoesNotLeak(t *testing.T) {
	s, _ := newMountSvc(t, "")
	id1 := s.newQrID()
	id2 := s.newQrID()
	if id1 == id2 {
		t.Fatalf("会话 id 撞了：%q", id1)
	}
	s.qrMu.Lock()
	s.qr[id1] = &qrSession{handle: "h1", expires: time.Now().Add(-time.Minute)}
	s.qr[id2] = &qrSession{handle: "h2", expires: time.Now().Add(time.Minute)}
	s.qrCleanLocked()
	_, keptOld := s.qr[id1]
	_, keptNew := s.qr[id2]
	s.qrMu.Unlock()
	if keptOld {
		t.Error("过期会话没有被清掉")
	}
	if !keptNew {
		t.Error("还在有效期内的会话被误删了")
	}

	s.dropQr(id2)
	s.qrMu.Lock()
	_, alive := s.qr[id2]
	s.qrMu.Unlock()
	if alive {
		t.Error("dropQr 没有生效")
	}
}
