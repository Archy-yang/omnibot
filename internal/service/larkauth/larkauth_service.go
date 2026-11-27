package larkauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"gorm.io/gorm"
	userdomain "omnibot/internal/domain/user"
	"omnibot/internal/pkg/backtask"
	"omnibot/internal/pkg/crypto"
)

// 语义化错误。
var (
	ErrNotFound      = errors.New("larkauth: not found")
	ErrInvalidInput  = errors.New("larkauth: app_id/app_secret 必填")
	ErrCryptoAbsent  = errors.New("larkauth: 加密密钥未初始化,无法保存凭证")
	ErrNotAuthorized = errors.New("larkauth: 用户尚未完成飞书授权")
)

// deviceCodeTTL device code 有效期。实测 lark-cli 1.0.94 `auth login --no-wait` 返回
// `expires_in: 600`;存进授权的 ExpiresAt,过期后由 sweeper 自动换发(v2.13)。
const deviceCodeTTL = 600 * time.Second

// Repo 持久化窄接口。
type Repo interface {
	GetByUserID(userID int64) (*userdomain.LarkCLIAuth, error)
	// ListByStatus 扫某状态的全部授权行(sweeper 驱动收尾;带 ctx 防扫库卡死)。
	ListByStatus(ctx context.Context, status string) ([]*userdomain.LarkCLIAuth, error)
	Upsert(row *userdomain.LarkCLIAuth) error
	Update(row *userdomain.LarkCLIAuth) error
	Delete(userID int64) error
}

// View 面向 API 的授权视图(secret/device_code 永不出层)。
type View struct {
	Status    string `json:"status"`
	AppID     string `json:"app_id"`
	OpenID    string `json:"open_id,omitempty"`
	UserName  string `json:"user_name,omitempty"`
	VerifyURL string `json:"verify_url,omitempty"`
	UserCode  string `json:"user_code,omitempty"`
	// ExpiresIn 验证链接剩余有效秒数(pending 时才有值,前端/IM 用来提示"还剩 N 分钟")。
	ExpiresIn int `json:"expires_in,omitempty"`
}

// healthTTL 健康检查结果缓存时长:设置抽屉反复打开时不必每次都 shell 出去问 CLI。
const healthTTL = 30 * time.Second

// healthEntry 一次健康检查的快照。
type healthEntry struct {
	view View
	at   time.Time
}

// Service 用户级 lark-cli 授权编排。
type Service struct {
	repo Repo
	cli  LarkCLI

	mu     sync.Mutex
	health map[int64]healthEntry
	now    func() time.Time

	// notifier 授权结果推送(nil = 只更新状态,不推送)。装配层注入。
	notifier Notifier
	// locks per-user 收尾互斥:Web 的 PollAuth 与后台 sweeper 共用,避免同一
	// device_code 被并发 poll(一个成功一个报 code 已用)。
	locks sync.Map // userID → *sync.Mutex
	// gate 后台收尾单飞:同一用户上一轮没跑完就跳过,不叠跑。
	gate *backtask.Gate
	// budget 单用户单轮收尾预算(默认 sweepBudget;测试可缩短)。
	budget time.Duration
}

func NewService(repo Repo, cli LarkCLI) *Service {
	return &Service{
		repo:   repo,
		cli:    cli,
		health: map[int64]healthEntry{},
		now:    time.Now,
		gate:   backtask.NewGate("larkauth-sweep"),
		budget: sweepBudget,
	}
}

// SetNotifier 注入授权结果推送器(v2.13 IM 内闭环)。nil 安全。
func (s *Service) SetNotifier(n Notifier) { s.notifier = n }

// userLock 取该用户的收尾互斥锁。
func (s *Service) userLock(userID int64) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(userID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// invalidateHealth 授权动作会改变真实态,清掉缓存。
func (s *Service) invalidateHealth(userID int64) {
	s.mu.Lock()
	delete(s.health, userID)
	s.mu.Unlock()
}

// expired 该授权行的 device code 是否已过期(未记录 ExpiresAt 视为未过期,兼容旧行)。
func (s *Service) expired(row *userdomain.LarkCLIAuth) bool {
	return !row.ExpiresAt.IsZero() && !s.now().Before(row.ExpiresAt)
}

// Health 健康检查:以 CLI 真实态为准校正 DB 状态(19 号状态机补强)。
//
// 背景:token 刷新完全由 lark-cli 负责(19 号 §1.1),我们只记"上次收尾成功";
// CLI 里的 user token 事后失效(容器重建/状态包丢失/CAS 冲突)时,DB 仍写 authorized,
// 造成"web 显示已授权、工具报未授权"。本方法把两者对齐:
//   - CLI 有可用 token → authorized(顺手补回 openId/userName)
//   - CLI 挂着未完成 device flow → 先尝试收尾,收不了标 need_relogin
//   - CLI 明确无 token → need_relogin
//
// CLI 探测本身失败(二进制缺失/超时)时不改状态——那是环境问题,不是授权失效。
func (s *Service) Health(ctx context.Context, userID int64) (View, error) {
	s.mu.Lock()
	if e, ok := s.health[userID]; ok && s.now().Sub(e.at) < healthTTL {
		s.mu.Unlock()
		return e.view, nil
	}
	s.mu.Unlock()

	row, err := s.repo.GetByUserID(userID)
	if err != nil {
		if isNotFound(err) {
			return s.cacheHealth(userID, View{Status: "none"}), nil
		}
		return View{}, err
	}
	// pending 由 sweeper / PollAuth 负责收尾,健康检查不抢
	if row.Status == userdomain.LarkAuthStatusPending {
		return s.cacheHealth(userID, s.viewOf(row)), nil
	}

	st, err := s.cli.AuthStatus(ctx, userID, row.ProfileName)
	if err != nil {
		log.Printf("larkauth: user=%d 健康检查失败(保持数据库状态): %v", userID, err)
		return s.cacheHealth(userID, s.viewOf(row)), nil
	}

	switch {
	case st.UserAvailable:
		changed := row.Status != userdomain.LarkAuthStatusAuthorized
		if st.OpenID != "" && st.OpenID != row.OpenID {
			row.OpenID = st.OpenID
			changed = true
		}
		if st.UserName != "" && st.UserName != row.UserName {
			row.UserName = st.UserName
			changed = true
		}
		row.Status = userdomain.LarkAuthStatusAuthorized
		row.DeviceCode, row.VerifiedURL = "", ""
		row.ExpiresAt = time.Time{}
		if changed {
			if err := s.repo.Update(row); err != nil {
				return View{}, err
			}
		}
	case st.DeviceCode != "":
		// CLI 侧挂着未完成的 device flow:尽力收尾一次(修掉"永远 pending 收不了尾"的死锁)
		if completeErr := s.cli.LoginComplete(ctx, userID, row.ProfileName, st.DeviceCode); completeErr == nil {
			if openID, name, stErr := s.cli.Status(ctx, userID, row.ProfileName); stErr == nil && openID != "" {
				row.Status = userdomain.LarkAuthStatusAuthorized
				row.OpenID, row.UserName = openID, name
				row.DeviceCode, row.VerifiedURL = "", ""
				row.ExpiresAt = time.Time{}
				if err := s.repo.Update(row); err != nil {
					return View{}, err
				}
				break
			}
		}
		row.Status = userdomain.LarkAuthStatusNeedRelogin
		if err := s.repo.Update(row); err != nil {
			return View{}, err
		}
	default:
		if row.Status != userdomain.LarkAuthStatusNeedRelogin {
			row.Status = userdomain.LarkAuthStatusNeedRelogin
			if err := s.repo.Update(row); err != nil {
				return View{}, err
			}
		}
	}
	return s.cacheHealth(userID, s.viewOf(row)), nil
}

// EnsureAuth 幂等发起 / 复用授权流程(v2.13:IM 内闭环的主入口)。
//
// 与 Relogin 的区别:Relogin 无条件开一个新 flow;EnsureAuth 先判断当前状态,
// 已授权就直接返回,未过期的 pending 就复用同一条验证链接——避免用户连着发几次
// 命令就把进行中的流程冲掉(旧实现每次都会 profile remove + 新 device code)。
//
// channel/target 记录"谁发起的",收尾成功后按它把结果推回同一个会话。
func (s *Service) EnsureAuth(ctx context.Context, userID int64, channel, target string) (View, error) {
	// 与后台收尾互斥:否则 sweeper 可能拿着旧 device_code 收尾失败,把刚发起的新流程覆盖成 failed
	mu := s.userLock(userID)
	mu.Lock()
	defer mu.Unlock()

	row, err := s.repo.GetByUserID(userID)
	if err != nil {
		if isNotFound(err) {
			return View{Status: "none"}, nil // 从未配置过应用:必须去 Web 填 AppID/Secret
		}
		return View{}, err
	}
	if channel != "" {
		row.NotifyChannel = channel
	}
	if target != "" {
		row.NotifyTarget = target
	}

	switch row.Status {
	case userdomain.LarkAuthStatusAuthorized:
		st, stErr := s.cli.AuthStatus(ctx, userID, row.ProfileName)
		if stErr != nil {
			// 探测失败是环境问题,不贸然作废授权、也不开新流程
			return View{}, fmt.Errorf("检查飞书授权状态失败: %w", stErr)
		}
		if st.UserAvailable {
			_ = s.repo.Update(row) // 持久化 notify 绑定,便于日后失效时能主动推回
			s.invalidateHealth(userID)
			return s.viewOf(row), nil
		}
		row.Status = userdomain.LarkAuthStatusNeedRelogin
	case userdomain.LarkAuthStatusPending:
		if row.DeviceCode != "" && !s.expired(row) {
			_ = s.repo.Update(row)
			return s.viewOf(row), nil
		}
	}

	secret, err := crypto.Decrypt(row.AppSecretEnc)
	if err != nil {
		return View{}, fmt.Errorf("larkauth: 解密 app secret 失败: %w", err)
	}
	return s.startAuth(ctx, userID, row.AppID, secret, channel, target, 0)
}

// Relogin 用库里已存的 AppID/AppSecret 重新发起 device flow(无需用户重填 secret)。
// Web 一键重授权走这里;IM 走 EnsureAuth(幂等)。
func (s *Service) Relogin(ctx context.Context, userID int64) (View, error) {
	mu := s.userLock(userID)
	mu.Lock()
	defer mu.Unlock()

	row, err := s.repo.GetByUserID(userID)
	if err != nil {
		if isNotFound(err) {
			return View{}, ErrNotAuthorized
		}
		return View{}, err
	}
	secret, err := crypto.Decrypt(row.AppSecretEnc)
	if err != nil {
		return View{}, fmt.Errorf("larkauth: 解密 app secret 失败: %w", err)
	}
	return s.startAuth(ctx, userID, row.AppID, secret, "web", "", 0)
}

func (s *Service) cacheHealth(userID int64, view View) View {
	s.mu.Lock()
	s.health[userID] = healthEntry{view: view, at: s.now()}
	s.mu.Unlock()
	return view
}

func profileFor(userID int64) string { return fmt.Sprintf("user-%d", userID) }

// StartAuth 发起授权:pending 行落库(secret 加密)→ profile add(幂等)→ LoginStart 回写 → 返回验证地址。
// 重复发起覆盖 pending(拿新 device code),仍只有一行。
func (s *Service) StartAuth(ctx context.Context, userID int64, appID, appSecret string) (View, error) {
	mu := s.userLock(userID)
	mu.Lock()
	defer mu.Unlock()
	return s.startAuth(ctx, userID, appID, appSecret, "web", "", 0)
}

// startAuth 发起授权的唯一实现。调用方必须已持有该用户的锁(见 userLock)。
// reissueCount 非 0 表示这是 sweeper 自动换发
// (保留累计值,用于限制自动换发次数);用户主动发起一律归零。
func (s *Service) startAuth(ctx context.Context, userID int64, appID, appSecret, channel, target string, reissueCount int) (View, error) {
	if appID == "" || appSecret == "" {
		return View{}, ErrInvalidInput
	}
	if !crypto.Configured() {
		return View{}, ErrCryptoAbsent
	}
	enc, err := crypto.Encrypt(appSecret)
	if err != nil {
		return View{}, fmt.Errorf("larkauth: 凭证加密失败: %w", err)
	}
	profile := profileFor(userID)
	if err := s.cli.ProfileAdd(ctx, userID, profile, appID, appSecret); err != nil {
		return View{}, err
	}
	verifyURL, userCode, deviceCode, err := s.cli.LoginStart(ctx, userID, profile, "") // 空 = CLI 默认域集合
	if err != nil && isMissingSecretError(err) {
		// 存量壳 profile(secret 没落盘,如从宿主机迁移来的):删除重建后重试一次。
		// 这是唯一会清凭证的路径,但此时凭据本来就是坏的,重建无损失。
		if rbErr := s.cli.ProfileRebuild(ctx, userID, profile, appID, appSecret); rbErr != nil {
			return View{}, rbErr
		}
		verifyURL, userCode, deviceCode, err = s.cli.LoginStart(ctx, userID, profile, "")
	}
	if err != nil {
		return View{}, err
	}
	// 重新授权(Relogin)时保留上一次的身份回显:Upsert 是整行覆盖,
	// 不清空的话 pending 界面会短暂变成"无名氏"。
	var prevOpenID, prevUserName, prevChannel, prevTarget string
	if prev, perr := s.repo.GetByUserID(userID); perr == nil && prev != nil {
		prevOpenID, prevUserName = prev.OpenID, prev.UserName
		prevChannel, prevTarget = prev.NotifyChannel, prev.NotifyTarget
	}
	if channel == "" {
		channel = prevChannel
	}
	if target == "" {
		target = prevTarget
	}
	row := &userdomain.LarkCLIAuth{
		UserID: userID, ProfileName: profile, AppID: appID, AppSecretEnc: enc,
		OpenID: prevOpenID, UserName: prevUserName,
		Status:        userdomain.LarkAuthStatusPending,
		DeviceCode:    deviceCode,
		VerifiedURL:   verifyURL,
		NotifyChannel: channel,
		NotifyTarget:  target,
		ExpiresAt:     s.now().Add(deviceCodeTTL),
		ReissueCount:  reissueCount,
	}
	if err := s.repo.Upsert(row); err != nil {
		return View{}, err
	}
	s.invalidateHealth(userID)
	return View{
		Status:    row.Status,
		AppID:     appID,
		VerifyURL: verifyURL,
		UserCode:  userCode,
		ExpiresIn: int(deviceCodeTTL.Seconds()),
	}, nil
}

// PollAuth 惰性收尾 + 状态查询:pending → LoginComplete(短超时)→ 按结果分类。
// 成败判定不看出收尾的退出码——实测 lark-cli 收尾成功也可能非零退出(exit 3);
// 权威判据是 whoami 的 token 是否就绪。非 pending 或无行直接返回现状。
//
// 并发纪律(v2.13):与后台 sweeper 共用 per-user 锁。拿不到锁说明收到尾正在跑,
// 直接返回 DB 现状而不排队——前端轮询下一轮自然会看到结果。
func (s *Service) PollAuth(ctx context.Context, userID int64) (View, error) {
	mu := s.userLock(userID)
	if !mu.TryLock() {
		return s.View(userID)
	}
	defer mu.Unlock()
	return s.pollAuthLocked(ctx, userID)
}

// pollAuthLocked PollAuth 的核心,调用方必须已持有该用户的锁。
func (s *Service) pollAuthLocked(ctx context.Context, userID int64) (View, error) {
	row, err := s.repo.GetByUserID(userID)
	if err != nil {
		if isNotFound(err) {
			return View{Status: "none"}, nil
		}
		return View{}, err
	}
	if row.Status != userdomain.LarkAuthStatusPending {
		return s.viewOf(row), nil
	}
	completeErr := s.cli.LoginComplete(ctx, userID, row.ProfileName, row.DeviceCode)
	// 单次探测超时 = 用户还没确认,不必再查身份(省掉一次无用的 whoami 往返)
	if errors.Is(completeErr, context.DeadlineExceeded) || isTimeout(completeErr) {
		return s.viewOf(row), nil
	}
	openID, userName, stErr := s.cli.Status(ctx, userID, row.ProfileName)
	switch {
	case stErr == nil && openID != "":
		// token 就绪即授权成功(用户已确认)
		row.Status = userdomain.LarkAuthStatusAuthorized
		row.OpenID, row.UserName = openID, userName
		row.DeviceCode, row.VerifiedURL = "", ""
		row.ExpiresAt = time.Time{}
		row.ReissueCount = 0
	case completeErr == nil && stErr != nil:
		// 收尾正常结束但 whoami 暂时失败:保持 pending,下轮 poll 再补回显
		log.Printf("larkauth: user=%d 收尾完成但 whoami 失败(下轮重试): %v", userID, stErr)
		return s.viewOf(row), nil
	default:
		// CLI 明确报错(code 过期/无效等)→ failed
		row.Status = userdomain.LarkAuthStatusFailed
		row.DeviceCode, row.VerifiedURL = "", ""
		row.ExpiresAt = time.Time{}
	}
	if err := s.repo.Update(row); err != nil {
		return View{}, err
	}
	s.invalidateHealth(userID)
	return s.viewOf(row), nil
}

// Disconnect 断开授权:logout(失败容忍,幂等自愈) + 删行。
func (s *Service) Disconnect(ctx context.Context, userID int64) error {
	mu := s.userLock(userID)
	mu.Lock()
	defer mu.Unlock()

	row, err := s.repo.GetByUserID(userID)
	if err != nil {
		return err
	}
	s.invalidateHealth(userID)
	if logoutErr := s.cli.Logout(ctx, userID, row.ProfileName); logoutErr != nil {
		log.Printf("larkauth: user=%d logout 失败(继续删行): %v", userID, logoutErr)
	}
	return s.repo.Delete(userID)
}

// ProfileForUser 执行侧解析:authorized → profile 名;否则 ErrNotAuthorized。
func (s *Service) ProfileForUser(userID int64) (string, error) {
	row, err := s.repo.GetByUserID(userID)
	if err != nil || row.Status != userdomain.LarkAuthStatusAuthorized {
		return "", ErrNotAuthorized
	}
	return row.ProfileName, nil
}

// View / StatusView 当前授权状态(不触发收尾,纯读)。
func (s *Service) View(userID int64) (View, error) {
	row, err := s.repo.GetByUserID(userID)
	if err != nil {
		if isNotFound(err) {
			return View{Status: "none"}, nil
		}
		return View{}, err
	}
	return s.viewOf(row), nil
}

func (s *Service) viewOf(row *userdomain.LarkCLIAuth) View {
	v := View{Status: row.Status, AppID: row.AppID, OpenID: row.OpenID, UserName: row.UserName}
	if row.Status == userdomain.LarkAuthStatusPending {
		v.VerifyURL = row.VerifiedURL
		// user_code 不单独存库:它就藏在验证链接的 query 里,回显时解析出来即可
		if u, err := url.Parse(row.VerifiedURL); err == nil {
			v.UserCode = u.Query().Get("user_code")
		}
		if !row.ExpiresAt.IsZero() {
			if left := row.ExpiresAt.Sub(s.now()); left > 0 {
				v.ExpiresIn = int(left.Seconds())
			}
		}
	}
	return v
}

// isNotFound 兼容两种 not found:mock 的 ErrNotFound 与 GORM 的 ErrRecordNotFound。
func isNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, gorm.ErrRecordNotFound)
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	var te interface{ Timeout() bool }
	if errors.As(err, &te) {
		return te.Timeout()
	}
	return false
}

// jsonMarshal 供测试脱敏断言使用。
func jsonMarshal(v interface{}) ([]byte, error) { return json.Marshal(v) }
