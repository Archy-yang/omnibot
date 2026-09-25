package user

import (
	"errors"
	"gorm.io/gorm"

	"omnibot/internal/domain/user"
)

// 编译期接口满足断言(service/user.AuthRepository)
var _ interface {
	CreateEmailAccount(email, passwordHash string) (int64, error)
	FindEmailChannel(email string) (*user.UserChannel, error)
	GetPasswordCredential(userID int64) (*user.UserCredential, error)
	GetUser(userID int64) (*user.User, error)
} = (*GormAuthRepository)(nil)

// GormAuthRepository AuthRepository 的 GORM 实现(service/user.AuthRepository)。
// 2026-09-25 §5.4 整改:原先 AuthService 直持 *gorm.DB 跨三表跑事务;
// 现事务边界收进本实现,service 层不感知 ORM。
// 实现不 import service 层(结构化类型隐式满足接口);唯一冲突转译为
// domain 哨兵 ErrEmailAlreadyExists(定义在 domain,避免 repo→service 反向依赖)。
type GormAuthRepository struct {
	db *gorm.DB
}

// NewAuthRepository 创建认证仓储。
func NewAuthRepository(db *gorm.DB) *GormAuthRepository {
	return &GormAuthRepository{db: db}
}

// CreateEmailAccount 原子创建 User + email UserChannel + UserCredential。
// (channel_type, channel_user_id) 唯一索引冲突 → ErrEmailAlreadyExists,整体回滚。
func (r *GormAuthRepository) CreateEmailAccount(email, passwordHash string) (int64, error) {
	var newUserID int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		u := user.NewUser()
		if err := tx.Create(u).Error; err != nil {
			return err
		}

		ch := user.NewUserChannel(u.ID, "email", email)
		if err := tx.Create(ch).Error; err != nil {
			// 唯一索引冲突 → 邮箱已注册(TranslateError 开启时为 ErrDuplicatedKey)
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return user.ErrEmailAlreadyExists
			}
			return err
		}

		cred := &user.UserCredential{
			UserID:       u.ID,
			PasswordHash: passwordHash,
		}
		if err := tx.Create(cred).Error; err != nil {
			return err
		}

		newUserID = u.ID
		return nil
	})
	if err != nil {
		return 0, err
	}
	return newUserID, nil
}

// FindEmailChannel 查 email 通道;不存在返回 (nil, nil)。
func (r *GormAuthRepository) FindEmailChannel(email string) (*user.UserChannel, error) {
	var ch user.UserChannel
	err := r.db.Where("channel_type = ? AND channel_user_id = ?", "email", email).First(&ch).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ch, nil
}

// GetPasswordCredential 查用户密码凭证;不存在返回 (nil, nil)。
func (r *GormAuthRepository) GetPasswordCredential(userID int64) (*user.UserCredential, error) {
	var cred user.UserCredential
	err := r.db.Where("user_id = ?", userID).First(&cred).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &cred, nil
}

// GetUser 查用户;不存在返回 (nil, nil)。
func (r *GormAuthRepository) GetUser(userID int64) (*user.User, error) {
	var u user.User
	err := r.db.First(&u, userID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}
