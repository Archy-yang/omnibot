package larkauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"gorm.io/gorm"
	userdomain "omnibot/internal/domain/user"
	"omnibot/internal/pkg/crypto"
)

// 语义化错误。
var (
	ErrNotFound      = errors.New("larkauth: not found")
	ErrInvalidInput  = errors.New("larkauth: app_id/app_secret 必填")
	ErrCryptoAbsent  = errors.New("larkauth: 加密密钥未初始化,无法保存凭证")
	ErrNotAuthorized = errors.New("larkauth: 用户尚未完成飞书授权")
)

// Repo 持久化窄接口。
type Repo interface {
	GetByUserID(userID int64) (*userdomain.LarkCLIAuth, error)
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
}

func NewService(repo Repo, cli LarkCLI) *Service {
	return &Service{repo: repo, cli: cli, health: map[int64]healthEntry{}, now: time.Now}
}

// invalidateHealth 授权动作会改变真实态,清掉缓存。
func (s *Service) invalidateHealth(userID int64) {
	s.mu.Lock()
	delete(s.health, userID)
	s.mu.Unlock()
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
	// pending 由 PollAuth 的惰性收尾负责,健康检查不抢
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
		if changed {
			if err := s.repo.Update(row); err != nil {
				return View{}, err
			}
		}
	case st.DeviceCode != "":
		// CLI 侧挂着未完成的 device flow:尽力收尾一次(修掉"永远 pending 收不了尾"的死锁)
		if completeErr := s.cli.LoginComplete(ctx, userID, row.ProfileName, st.DeviceCode); completeErr == nil {
			if openID, name, stErr := s.cli.Status(userID, row.ProfileName); stErr == nil && openID != "" {
				row.Status = userdomain.LarkAuthStatusAuthorized
				row.OpenID, row.UserName = openID, name
				row.DeviceCode, row.VerifiedURL = "", ""
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

// Relogin 用库里已存的 AppID/AppSecret 重新发起 device flow(无需用户重填 secret)。
func (s *Service) Relogin(ctx context.Context, userID int64) (View, error) {
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
	return s.StartAuth(ctx, userID, row.AppID, secret)
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
	if err := s.cli.ProfileAdd(userID, profile, appID, appSecret); err != nil {
		return View{}, err
	}
	verifyURL, userCode, deviceCode, err := s.cli.LoginStart(userID, profile, "") // 空 = CLI 默认域集合
	if err != nil {
		return View{}, err
	}
	row := &userdomain.LarkCLIAuth{
		UserID: userID, ProfileName: profile, AppID: appID, AppSecretEnc: enc,
		Status:     userdomain.LarkAuthStatusPending,
		DeviceCode: deviceCode, VerifiedURL: verifyURL,
	}
	if err := s.repo.Upsert(row); err != nil {
		return View{}, err
	}
	s.invalidateHealth(userID)
	return View{Status: row.Status, AppID: appID, VerifyURL: verifyURL, UserCode: userCode}, nil
}

// PollAuth 惰性收尾 + 状态查询:pending → LoginComplete(短超时)→ 按结果分类。
// 成败判定不看出收尾的退出码——实测 lark-cli 收尾成功也可能非零退出(exit 3);
// 权威判据是 whoami 的 token 是否就绪。非 pending 或无行直接返回现状。
func (s *Service) PollAuth(ctx context.Context, userID int64) (View, error) {
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
	openID, userName, stErr := s.cli.Status(userID, row.ProfileName)
	switch {
	case stErr == nil && openID != "":
		// token 就绪即授权成功(用户已确认)
		row.Status = userdomain.LarkAuthStatusAuthorized
		row.OpenID, row.UserName = openID, userName
		row.DeviceCode, row.VerifiedURL = "", ""
	case errors.Is(completeErr, context.DeadlineExceeded) || isTimeout(completeErr):
		// 单次探测超时=还没确认:保持 pending 原样(验证链接仍在),下轮 poll 再问
		return s.viewOf(row), nil
	case completeErr == nil && stErr != nil:
		// 收尾正常结束但 whoami 暂时失败:保持 pending,下轮 poll 再补回显
		log.Printf("larkauth: user=%d 收尾完成但 whoami 失败(下轮重试): %v", userID, stErr)
		return s.viewOf(row), nil
	default:
		// CLI 明确报错(code 过期/无效等)→ failed
		row.Status = userdomain.LarkAuthStatusFailed
		row.DeviceCode, row.VerifiedURL = "", ""
	}
	if err := s.repo.Update(row); err != nil {
		return View{}, err
	}
	s.invalidateHealth(userID)
	return s.viewOf(row), nil
}

// Disconnect 断开授权:logout(失败容忍,幂等自愈) + 删行。
func (s *Service) Disconnect(ctx context.Context, userID int64) error {
	row, err := s.repo.GetByUserID(userID)
	if err != nil {
		return err
	}
	s.invalidateHealth(userID)
	if logoutErr := s.cli.Logout(userID, row.ProfileName); logoutErr != nil {
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
		v.VerifyURL, v.UserCode = row.VerifiedURL, ""
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
