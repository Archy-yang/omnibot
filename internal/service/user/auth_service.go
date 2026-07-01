// Package user 的 AuthService 提供邮箱密码注册/登录。
package user

import (
	"errors"
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"omnibot/internal/domain/user"
	"omnibot/internal/pkg/auth"
)

// 认证相关 sentinel errors,handler 层用 errors.Is 判断后映射到用户提示
var (
	// ErrEmailInvalid 邮箱格式非法或超长
	ErrEmailInvalid = errors.New("email invalid")
	// ErrPasswordInvalid 密码长度不在 8~64 位
	ErrPasswordInvalid = errors.New("password invalid")
	// ErrEmailAlreadyExists 邮箱已被注册
	ErrEmailAlreadyExists = errors.New("email already exists")
	// ErrInvalidCredentials 邮箱或密码错误(统一提示,防枚举)
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrAccountUnavailable 账号被封禁 / 已删除
	ErrAccountUnavailable = errors.New("account unavailable")
)

// 邮箱格式:粗校验,含且仅含一个 @ 且域名部分含 .
var emailRegexp = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// 邮箱最大长度(RFC 5321)
const emailMaxLen = 254

// 密码长度
const (
	passwordMinLen = 8
	passwordMaxLen = 64
)

// AuthService 邮箱密码认证服务
//
// 直接持有 *gorm.DB 用于事务(Register 需要跨 3 张表:users / user_channels / user_credentials)。
// 事务边界收敛在这一处,避免为一次性事务改动 4 个 repo 接口。
type AuthService struct {
	db  *gorm.DB
	jwt *auth.JWTService
}

// NewAuthService 创建 AuthService
func NewAuthService(db *gorm.DB, jwtSvc *auth.JWTService) *AuthService {
	return &AuthService{db: db, jwt: jwtSvc}
}

// Register 注册邮箱账号。成功返回签发的 JWT(自动登录)。
//
// 事务:创建 User + UserChannel(email) + UserCredential,任一步失败整体回滚。
// 邮箱归一化(trim + 小写),唯一索引兜底并发重复。
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

	var newUserID int64
	err = s.db.Transaction(func(tx *gorm.DB) error {
		u := user.NewUser()
		if err := tx.Create(u).Error; err != nil {
			return err
		}

		ch := user.NewUserChannel(u.ID, "email", normalized)
		if err := tx.Create(ch).Error; err != nil {
			// (channel_type, channel_user_id) 唯一索引冲突 → 邮箱已注册
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return ErrEmailAlreadyExists
			}
			return err
		}

		cred := &user.UserCredential{
			UserID:       u.ID,
			PasswordHash: string(hash),
		}
		if err := tx.Create(cred).Error; err != nil {
			return err
		}

		newUserID = u.ID
		return nil
	})
	if err != nil {
		return "", err
	}

	return s.jwt.GenerateToken(newUserID)
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
	var ch user.UserChannel
	err = s.db.Where("channel_type = ? AND channel_user_id = ?", "email", normalized).First(&ch).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrInvalidCredentials
		}
		return "", err
	}

	// 2. 查 credential
	var cred user.UserCredential
	err = s.db.Where("user_id = ?", ch.UserID).First(&cred).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 理论上不该发生(注册事务保证同时建),但兜底
			return "", ErrInvalidCredentials
		}
		return "", err
	}

	// 3. 比对密码
	if err := bcrypt.CompareHashAndPassword([]byte(cred.PasswordHash), []byte(password)); err != nil {
		return "", ErrInvalidCredentials
	}

	// 4. 检查用户状态
	var u user.User
	err = s.db.First(&u, ch.UserID).Error
	if err != nil {
		return "", err
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
