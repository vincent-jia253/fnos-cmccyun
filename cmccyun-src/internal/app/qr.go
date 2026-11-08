package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/vincent-jia253/fnos-cmccyun/internal/api"
)

// 扫码登录的控制台侧封装。
//
// 分工：internal/api 负责和 139 握手（密码学部分），这里只做三件事——
//  1. 把 handle（里面含 skey）留在服务端，浏览器只拿到一个随机 id；
//  2. 把二维码内容渲染成 PNG，以 data URI 内嵌给前端，不依赖外网 CDN；
//  3. 确认成功后把 Authorization 写进配置，并顺手自动挂载。
//
// 会话只活 5 分钟且只存内存：进程一重启就得重扫，但 skey 也因此不会落盘。

type qrSession struct {
	handle  string
	expires time.Time
	// acct 是这次扫码要写到哪个账号上（多账号下每台云盘各自扫一次）。
	acct string
	// lastStatus/lastCode 只用来"变化时打一行日志"：扫码失败时（比如扫一下就失效）
	// 得能一眼看出上游到底回了什么，否则只能靠猜。
	lastStatus string
	lastCode   string
}

// StartQrLogin 生成一次扫码会话，返回 id、二维码 PNG 和有效期（秒）。
func (s *Service) StartQrLogin(ctx context.Context, id string) (string, []byte, int, error) {
	cfg := s.Config()
	ac, ok := cfg.AccountByID(id)
	if !ok {
		return "", nil, 0, fmt.Errorf("账号不存在：%s", id)
	}
	start, err := api.StartQrLogin(ctx)
	if err != nil {
		s.logf("扫码登录：获取二维码失败：%v", err)
		return "", nil, 0, err
	}
	// 360 像素：屏幕上看清楚，手机也扫得动。
	png, err := qrcode.Encode(start.Content, qrcode.Medium, 360)
	if err != nil {
		return "", nil, 0, fmt.Errorf("生成二维码图片失败: %w", err)
	}
	sid := s.newQrID()
	s.qrMu.Lock()
	s.qrCleanLocked()
	s.qr[sid] = &qrSession{handle: start.Handle, expires: time.Now().Add(start.ExpiresIn), acct: ac.ID}
	s.qrMu.Unlock()
	secs := int(start.ExpiresIn.Seconds())
	s.logf("扫码登录：已生成二维码（账号 %s），%d 秒内有效", ac.DisplayName(), secs)
	return sid, png, secs, nil
}

// PollQrLogin 问一次状态，返回值直接给前端渲染。
func (s *Service) PollQrLogin(ctx context.Context, id string) (map[string]any, error) {
	s.qrMu.Lock()
	sess := s.qr[id]
	s.qrMu.Unlock()
	if sess == nil {
		return map[string]any{
			"ok":      true,
			"status":  string(api.QrExpired),
			"message": "扫码会话已失效，请重新生成二维码",
		}, nil
	}
	if !sess.expires.IsZero() && time.Now().After(sess.expires) {
		s.dropQr(id)
		return map[string]any{
			"ok":      true,
			"status":  string(api.QrExpired),
			"message": "二维码已过期，请重新生成",
		}, nil
	}

	res, err := api.PollQrLogin(ctx, sess.handle)
	if err != nil {
		s.logf("扫码登录：轮询失败：%v", err)
		return nil, err
	}
	// 状态一变就记一行（含上游原始码），排查"扫码就失效"时直接看日志。
	s.qrMu.Lock()
	if cur := s.qr[id]; cur != nil && (cur.lastStatus != string(res.Status) || cur.lastCode != res.Code) {
		cur.lastStatus, cur.lastCode = string(res.Status), res.Code
		s.logf("扫码登录：状态 %s（上游 code=%s）", res.Status, res.Code)
	}
	s.qrMu.Unlock()

	out := map[string]any{
		"ok":      true,
		"status":  string(res.Status),
		"message": res.Message,
		"account": res.Account,
	}
	switch res.Status {
	case api.QrConfirmed:
		s.dropQr(id)
		if err := s.applyQrAuth(sess.acct, res.Authorization); err != nil {
			return nil, err
		}
		out["authorized"] = true
		s.logf("扫码登录成功：账号 %s，授权已保存", res.Account)
		// 挂载要几秒到几十秒，别把前端轮询卡住
		go s.mountAfterLogin(sess.acct)
	case api.QrExpired, api.QrCancelled:
		s.dropQr(id)
	}
	return out, nil
}

// applyQrAuth 把扫码拿到的 base64("pc:账号:令牌") 落到配置里。走的和手工粘贴
// 完全同一个入口，所以续期、状态卡、挂载逻辑都不用改。
func (s *Service) applyQrAuth(id, authorization string) error {
	authorization = strings.TrimSpace(authorization)
	if authorization == "" {
		return fmt.Errorf("扫码结果里没有授权信息")
	}
	if err := s.UpdateOf(id, map[string]any{"authorization": authorization}); err != nil {
		return fmt.Errorf("保存授权失败: %w", err)
	}
	return nil
}

// mountAfterLogin 在扫码成功后自动挂一次，省掉「还得手动点挂载」。
func (s *Service) mountAfterLogin(id string) {
	st := s.StatusOf(id)
	if st.Mounted {
		return
	}
	if strings.TrimSpace(st.MountPoint) == "" {
		s.logf("扫码登录：还没设置挂载点，去「挂载」页填一个就能自动挂上")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if err := s.MountOf(ctx, id); err != nil {
		s.logf("扫码登录后自动挂载失败：%v（可在控制台点「挂载」重试）", err)
		return
	}
	s.logf("扫码登录后已自动挂载到 %s", s.StatusOf(id).MountPoint)
}

func (s *Service) newQrID() string {
	s.qrMu.Lock()
	defer s.qrMu.Unlock()
	if s.qr == nil {
		s.qr = map[string]*qrSession{}
	}
	for {
		s.qrSeq++
		id := fmt.Sprintf("qr%d-%d", time.Now().UnixMilli(), s.qrSeq)
		if _, exists := s.qr[id]; !exists {
			return id
		}
	}
}

func (s *Service) dropQr(id string) {
	s.qrMu.Lock()
	delete(s.qr, id)
	s.qrMu.Unlock()
}

// qrCleanLocked 顺手清掉过期会话，避免长跑之后 map 越堆越大。
func (s *Service) qrCleanLocked() {
	now := time.Now()
	for id, sess := range s.qr {
		if sess == nil || (!sess.expires.IsZero() && now.After(sess.expires)) {
			delete(s.qr, id)
		}
	}
}
