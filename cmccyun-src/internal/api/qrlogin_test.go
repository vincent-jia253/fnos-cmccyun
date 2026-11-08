package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// 轮询请求体必须是网页版那一条：{account:"", dycPwd:<sID>, type:5, authLogin:true}
// 经 AES-256-CBC 包成 base64 直接当请求体（不是 {"encryptMsg":…} 信封）。
// 老实现发的是飞信 {dycpwd,secinfo,cpid,clienttype,pintype:21}，上游回 200059541
// 「未找到设备的二维码信息」——所以二维码根本没登记，手机扫到就提示失效。
func TestQrPollBodyShape(t *testing.T) {
	sid := "jsTQHwEch1g3AKpM"
	body, err := qrPollBody(sid)
	if err != nil {
		t.Fatal(err)
	}
	// 1) 不能是 JSON（JSON 会让上游报 "Illegal base64 character 7b"）
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("{")) {
		t.Fatalf("请求体不该是 JSON：%s", body)
	}
	// 2) 解出来必须是那条 payload
	plain, err := qrDecryptCBC(string(body))
	if err != nil {
		t.Fatalf("解不开自己发的密文：%v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(plain, &got); err != nil {
		t.Fatalf("明文不是 JSON：%v（%q）", err, plain)
	}
	if got["dycPwd"] != sid {
		t.Errorf("dycPwd 应为 %q，得到 %v", sid, got["dycPwd"])
	}
	if got["type"] != float64(5) {
		t.Errorf("type 应为 5，得到 %v", got["type"])
	}
	if got["authLogin"] != true {
		t.Errorf("authLogin 应为 true，得到 %v", got["authLogin"])
	}
	if _, has := got["account"]; !has {
		t.Error("payload 里要有 account 字段（空字符串）")
	}
	// 老字段一个都不该再出现
	for _, legacy := range []string{"dycpwd", "secinfo", "pintype", "cpid", "clienttype"} {
		if _, has := got[legacy]; has {
			t.Errorf("不该再带老字段 %q", legacy)
		}
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
