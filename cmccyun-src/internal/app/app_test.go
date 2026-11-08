package app

import (
	"strings"
	"testing"
)

// 用户从浏览器抄 Authorization 的姿势五花八门，这里统一收拾干净。
func TestNormalizeAuthorization(t *testing.T) {
	want := "cGM6MTM4MDAwMDAwMDA6dG9rZW4="
	cases := []string{
		want,
		"  " + want + "  ",
		"Basic " + want,
		"Authorization: Basic " + want,
		"authorization:basic " + want,
		"Authorization: " + want,
		"\"" + want + "\"",
		"cGM6MTM4MDAwMDAwMDA6\ndG9rZW4=",  // 被编辑器折成两行
		"cGM6MTM4 MDAwMDAw MDA6dG9r ZW4=", // 中间被插了空格
	}
	for _, in := range cases {
		got := normalizeAuthorization(in)
		if got != want {
			t.Errorf("输入 %q 收拾后是 %q，期望 %q", in, got, want)
		}
	}
	// 空输入保持空（不能把"清空授权"变成写入垃圾）
	if got := normalizeAuthorization("   "); got != "" {
		t.Errorf("空白输入应为空，得到 %q", got)
	}
}

// 收拾干净以后必须能过 api.New 的校验（形状：base64 的 pc:账号:令牌）。
func TestNormalizeAuthorizationEndToEnd(t *testing.T) {
	s, _ := newMountSvc(t, "")
	raw := "Authorization: Basic " + "cGM6MTM4MDAwMDAwMDA6dG9rZW4="
	if err := s.UpdateOf("", map[string]any{"authorization": raw}); err != nil {
		t.Fatalf("带外壳的 Authorization 应能保存：%v", err)
	}
	ac := acctView(s.Config())
	if ac.Authorization != "cGM6MTM4MDAwMDAwMDA6dG9rZW4=" {
		t.Errorf("落盘的值没收拾干净：%q", ac.Authorization)
	}
	if ac.Account != "13800000000" {
		t.Errorf("账号没解析出来：%q", ac.Account)
	}
}

// 界面上的那行说明要与实际行为一致（不能还写着"必须粘一整条 base64"）。
func TestAuthHintMentionsPasteTolerance(t *testing.T) {
	for _, want := range []string{
		"Authorization",
		"粘贴",
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("授权页缺少 %q 的说明", want)
		}
	}
}

// 授权页要给出「不靠扫码也能拿到授权」的路径：书签小程序 + 复制按钮。
func TestAuthPageHasBookmarklet(t *testing.T) {
	for _, want := range []string{
		`id="bmCode"`,
		`id="btnCopyBm"`,
		"localStorage.getItem(\"authorization\")",
		"javascript:(function(){",
		"扫码扫不上",
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("授权页缺少 %q", want)
		}
	}
	// 书签代码里不能出现反引号（ui.go 是 Go 原始字符串）
	if strings.Contains(indexHTML, "`") {
		t.Error("indexHTML 里出现了反引号")
	}
}
