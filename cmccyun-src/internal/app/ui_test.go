package app

import (
	"regexp"
	"strings"
	"testing"
)

// 背景（v1.3.0 上线后被用户抓到的真实 bug）：
// 顶栏和「云盘浏览」卡片标题里用 <div class="sp"> 当撑开的占位元素，
// 而 .sp 是按钮里的加载指示器（13px 的圆环 + rot 无限旋转）。
// 两者撞类名 → 占位元素被渲染成一个 600+ px 宽的旋转圆弧，
// 用户看到的就是「有个线一直在转」。占位必须用 .grow。
//
// 这个测试把「.sp 只能作为按钮里的 <i>」写成断言，避免以后再犯。
func TestSpinnerClassOnlyUsedInsideButtons(t *testing.T) {
	tagRe := regexp.MustCompile(`<([a-zA-Z0-9]+)([^>]*class="sp"[^>]*)>`)
	matches := tagRe.FindAllStringSubmatch(indexHTML, -1)
	if len(matches) == 0 {
		t.Fatalf("页面里一个 .sp 加载指示器都没有？八成是被误删了")
	}
	for _, m := range matches {
		tag, attrs := m[1], m[2]
		if strings.ToLower(tag) != "i" {
			t.Errorf("class=\"sp\" 只能用在 <i> 上，这里用的是 <%s%s> —— "+
				"占位请改用 class=\"grow\"，否则会渲染成一个巨大的旋转圆弧", tag, attrs)
		}
		if strings.Contains(attrs, "flex") {
			t.Errorf("<%s%s> 把 .sp 当占位元素用了（带 flex），请改用 class=\"grow\"", tag, attrs)
		}
	}
}

// 用户在控制台看到「授权 剩余 29.7 天」会问这是什么 —— 页面必须自带解释：
// 顶栏徽标 / 状态卡 / 授权页都要有悬停说明，授权页还要有一段看得见的文字说明。
func TestAuthDaysHasTooltipAndExplanation(t *testing.T) {
	for _, want := range []string{
		"var AUTH_TIP",                     // 说明文案集中在一处
		`$("badgeAuth").title = AUTH_TIP;`, // 顶栏徽标有悬停说明
		"不是会员到期日",                          // 说清它不是会员到期
		"15 天",                             // 说清自动续期的触发条件
		"自动续期",                             // 说清会自动续期
		`title="' + esc(AUTH_TIP) + '"`,    // 状态卡里的「授权剩余」也有说明
		"顶栏「授权 剩余 N 天」指的是什么？", // 授权页有看得见的说明（手机上没有悬停）
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("界面缺少授权天数的说明：找不到 %q", want)
		}
	}
}

// .sp 的样式必须是「小圆环」，不能被任何布局规则重新定义成撑开的占位。
func TestSpinnerStylesAreNotOverriddenByLayoutRules(t *testing.T) {
	if strings.Contains(indexHTML, ".topbar .sp{") {
		t.Error("存在 .topbar .sp{...} 规则：这会把加载指示器变成顶栏占位元素（v1.3.0 的 bug）")
	}
	if !strings.Contains(indexHTML, ".grow{flex:1 1 auto;}") {
		t.Error("缺少 .grow 占位工具类（撑开用），当前页面可能又在用 .sp 当占位")
	}
	if !strings.Contains(indexHTML, ".sp{") {
		t.Error("找不到 .sp 的加载指示器样式")
	}
	// 加载指示器必须是无限旋转的小圆环
	i := strings.Index(indexHTML, ".sp{")
	if seg := indexHTML[i:]; !strings.Contains(seg[:min(240, len(seg))], "animation:rot") {
		t.Error(".sp 不再是旋转动画，加载态会没有反馈")
	}
}
