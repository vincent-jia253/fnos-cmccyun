package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// 轮询请求体必须是「飞信动态密码」那套（本机 /vol4/@appcenter/yun139 的
// server.mjs 里 server/src/provider/yun139/qrlogin.mjs 就是这么发的，它能扫码登录）。
// 曾经改成 {"account":"","dycPwd":…,"type":5} 把登录打断了，这个测试钉住不许再改回去。
func TestQrPollBodyShape(t *testing.T) {
	sid := "jsTQHwEch1g3AKpM"
	body, err := qrPollBody(sid)
	if err != nil {
		t.Fatal(err)
	}
	// 1) 不能是 JSON（上游要的是 base64 信封）
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("{")) {
		t.Fatalf("请求体不该是 JSON：%s", body)
	}
	// 2) 解出来必须是那套字段
	plain, err := qrDecryptCBC(string(body))
	if err != nil {
		t.Fatalf("解不开自己发的密文：%v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(plain, &got); err != nil {
		t.Fatalf("明文不是 JSON：%v（%q）", err, plain)
	}
	want := map[string]any{
		"dycpwd": sid, "cpid": float64(292), "clienttype": float64(670),
		"version": "mCloud_4.3.0_536", "pintype": float64(21), "loginMode": "0",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("字段 %s = %v，期望 %v", k, got[k], v)
		}
	}
	// secinfo 必须是 sha1("fetion.com.cn:"+sid) 的大写 hex（上游按这个识别会话）
	if got["secinfo"] != qrSecinfo(sid) {
		t.Errorf("secinfo = %v，期望 %v", got["secinfo"], qrSecinfo(sid))
	}
	if got["msisdn"] != "" || got["random"] != "" {
		t.Errorf("msisdn/random 必须为空字符串，得到 %v/%v", got["msisdn"], got["random"])
	}
	if _, has := got["extInfo"]; !has {
		t.Error("payload 里要有 extInfo 字段（空对象）")
	}
	// 3) 每次加密 IV 随机 → 两次密文不同
	body2, _ := qrPollBody(sid)
	if string(body) == string(body2) {
		t.Error("两次请求体应不同（随机 IV）")
	}
}

// 上游返回码的语义：这直接决定界面显示"等待"还是"已失效"。
func TestQrStatusFromCode(t *testing.T) {
	cases := []struct {
		code    string
		status  QrStatus
		wantMsg string
	}{
		{"200059548", QrScanned, "已扫码"},
		{"200059542", QrExpired, "过期"},
		{"200059549", QrCancelled, "取消"},
		{"200059543", QrFailed, "失败"},
		{"200059541", QrWaiting, "等待"}, // 未找到设备的二维码信息 = 还没人扫
		{"9999", QrWaiting, "等待"},      // 登录失败 = 手机还没确认
		{"", QrWaiting, "等待"},
	}
	for _, c := range cases {
		got := qrStatusFromCode(c.code, "")
		if got.Status != c.status {
			t.Errorf("code %q → %s，期望 %s", c.code, got.Status, c.status)
		}
		if !strings.Contains(got.Message, c.wantMsg) {
			t.Errorf("code %q 的提示 %q 里应包含 %q", c.code, got.Message, c.wantMsg)
		}
		if got.Code != c.code {
			t.Errorf("code %q 没被带出来：%q", c.code, got.Code)
		}
	}
	// 不认识的码：仍按"继续等"，但要把码带到提示里方便对日志
	odd := qrStatusFromCode("123456", "系统繁忙")
	if odd.Status != QrWaiting || !strings.Contains(odd.Message, "123456") {
		t.Errorf("未知码应继续等待并带上码，得到 %s / %s", odd.Status, odd.Message)
	}
}

// base64 一封到底：不能出现换行或非法字符（否则上游解不开）。
func TestQrPollBodyIsCleanBase64(t *testing.T) {
	body, err := qrPollBody("0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := base64.StdEncoding.DecodeString(string(body)); err != nil {
		t.Fatalf("请求体不是合法 base64：%v", err)
	}
}
