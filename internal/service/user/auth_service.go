// Package user 的 AuthService 提供邮箱密码注册/登录。
package user

import (
	"errors"
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"omnibot/internal/domain/user"
	"omnibot/internal/pkg/auth"
)

// 认证相关 sentinel errors,handler 层用 errors.Is 判断后映射到用户提示
var (
	// ErrEmailInvalid 邮箱格式非法或超长
	ErrEmailInvalid = errors.New("email invalid")
	// ErrPasswordInvalid 密码长度不在 8~64 位
	ErrPasswordInvalid = errors.New("password invalid")
	// ErrEmailAlreadyExists 邮箱已被注册(领域哨兵,repository 事务内唯一冲突转译用同一定义)
	ErrEmailAlreadyExists = user.ErrEmailAlreadyExists
	// ErrInvalidCredentials 邮箱或密码错误(统一提示,防枚举)
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrAccountUnavailable 账号被封禁 / 已删除
	ErrAccountUnavailable = errors.New("account unavailable")
)

// 邮箱格式:粗校验,含且仅含一个 @ 且域名部分含 .
var emailRegexp = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s]+$`)

// 邮箱最大长度(RFC 5321)
const emailMaxLen = 254

// 密码长度
const (
	passwordMinLen = 8
	passwordMaxLen = 64
)

// AuthRepository 认证持久化窄接口(service 层声明,repository 层实现,DeepSeek 审查 §5.4 整改):
// AuthService 不再持有 *gorm.DB——事务边界收进实现层的 CreateEmailAccount,
// service 层不感知 ORM 与其哨兵错误;查询"不存在"以 (nil, nil) 表达,
// 登录的防枚举语义(不存在/密码错误统一映射)由本层负责。
type AuthRepository interface {
	// CreateEmailAccount 原子创建邮箱账号(User + email channel + credential,同事务)。
	// 邮箱已注册返回 domainuser.ErrEmailAlreadyExists。
	CreateEmailAccount(email, passwordHash string) (userID int64, err error)
	// FindEmailChannel 查 email 通道;不存在返回 (nil, nil)。
	FindEmailChannel(email string) (*user.UserChannel, error)
	// GetPasswordCredential 查用户密码凭证;不存在返回 (nil, nil)。
	GetPasswordCredential(userID int64) (*user.UserCredential, error)
	// GetUser 查用户;不存在返回 (nil, nil)。
	GetUser(userID int64) (*user.User, error)
}

// AuthService 邮箱密码认证服务
type AuthService struct {
	repo AuthRepository
	jwt  *auth.JWTService
}

// NewAuthService 创建 AuthService
func NewAuthService(repo AuthRepository, jwtSvc *auth.JWTService) *AuthService {
	return &AuthService{repo: repo, jwt: jwtSvc}
}

// Register 注册邮箱账号。成功返回签发的 JWT(自动登录)。
//
// 账号三表创建由 repository 原子完成;邮箱归一化(trim + 小写),
// 唯一索引兜底并发重复(repository 转译为 ErrEmailAlreadyExists)。
func (s *AuthService) Register(email, password string) (string, error) {
	normalized, err := normalizeAndValidateEmail(email)
	if err != nil {
		return "", err
	}
	if err := validatePassword(password); err != nil {
		return "", err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}

	userID, err := s.repo.CreateEmailAccount(normalized, string(hash))
	if err != nil {
		return "", err
	}
	return s.jwt.GenerateToken(userID)
}

// Login 邮箱密码登录。
//
// 校验顺序:找到 email→查 hash→比对→检查状态。
// 失败一律返回 ErrInvalidCredentials(不区分"邮箱不存在"与"密码错误",防枚举)。
// 只有账号被封禁 / 删除时返回 ErrAccountUnavailable。
func (s *AuthService) Login(email, password string) (string, error) {
	normalized, err := normalizeAndValidateEmail(email)
	if err != nil {
		// 邮箱格式非法也归并为凭证错误,保持登录接口的"不透露信号"承诺
		return "", ErrInvalidCredentials
	}

	// 1. 找到 email → user_id
	ch, err := s.repo.FindEmailChannel(normalized)
	if err != nil {
		return "", err
	}
	if ch == nil {
		return "", ErrInvalidCredentials
	}

	// 2. 查 credential(理论上不该缺——注册事务保证同时建,缺则兜底归并)
	cred, err := s.repo.GetPasswordCredential(ch.UserID)
	if err != nil {
		return "", err
	}
	if cred == nil {
		return "", ErrInvalidCredentials
	}

	// 3. 比对密码
	if err := bcrypt.CompareHashAndPassword([]byte(cred.PasswordHash), []byte(password)); err != nil {
		return "", ErrInvalidCredentials
	}

	// 4. 检查用户状态
	u, err := s.repo.GetUser(ch.UserID)
	if err != nil {
		return "", err
	}
	if u == nil {
		return "", ErrInvalidCredentials
	}
	if u.Status != user.StatusNormal {
		return "", ErrAccountUnavailable
	}

	return s.jwt.GenerateToken(u.ID)
}

// normalizeAndValidateEmail 归一化(trim + ToLower)并校验格式与长度
func normalizeAndValidateEmail(raw string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(raw))
	if e == "" {
		return "", ErrEmailInvalid
	}
	if len(e) > emailMaxLen {
		return "", ErrEmailInvalid
	}
	if !emailRegexp.MatchString(e) {
		return "", ErrEmailInvalid
	}
	return e, nil
}

// validatePassword 校验密码长度 8~64,不 trim(空格是密码一部分)
func validatePassword(pw string) error {
	if len(pw) < passwordMinLen || len(pw) > passwordMaxLen {
		return ErrPasswordInvalid
	}
	return nil
}
