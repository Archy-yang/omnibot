// Package user 的 BindingService 实现账号绑定(v2.2 飞书引入,v2.3 泛化为渠道通用)。
package user

import (
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"omnibot/internal/domain/user"
	userrepo "omnibot/internal/repository/user"
)

// 绑定相关 sentinel errors,handler 层用 errors.Is 映射用户提示(PRD 5.2)
var (
	// ErrCodeInvalid 绑定码不存在或已过期
	ErrCodeInvalid = errors.New("bind code invalid or expired")
	// ErrChannelAlreadyBound 该渠道号(open_id)已绑定其他账号
	ErrChannelAlreadyBound = errors.New("channel account already bound")
	// ErrAccountAlreadyBound 该 web 账号已绑定过该渠道号
	ErrAccountAlreadyBound = errors.New("account already bound this channel")
)

// BindingService 账号绑定服务(渠道通用)。
//
// 绑定关系最终落到 user_channels(channel_type, open_id, user_id) 一行;
// v2.1 已为 (channel_type, channel_user_id) 建复合唯一索引,天然防「一号多账号」。
// 绑定码用 bind_codes 表持久化(5 分钟过期,upsert 作废旧码),码不区分渠道--
// 在哪个渠道发送就绑哪个渠道(通用码,v2.3)。
type BindingService struct {
	channelRepo  UserChannelRepository
	bindCodeRepo userrepo.BindCodeRepository
	codeTTL      time.Duration
}

// NewBindingService 创建 BindingService。
// codeTTL 建议 5 分钟(PRD 4.1)。
func NewBindingService(
	channelRepo UserChannelRepository,
	bindCodeRepo userrepo.BindCodeRepository,
	codeTTL time.Duration,
) *BindingService {
	return &BindingService{
		channelRepo:  channelRepo,
		bindCodeRepo: bindCodeRepo,
		codeTTL:      codeTTL,
	}
}

// GenerateCode 为 web 账号生成 6 位绑定码(通用,不区分渠道)。
// 重新生成会作废旧码(repo upsert 按 user_id 覆盖)。
// 注:不在生成阶段校验"账号已绑所有渠道"--一个账号可同时绑飞书+微信,
// 出码用于绑尚未绑的那个渠道;若两渠道都已绑,web handler 层拦截不出码。
func (s *BindingService) GenerateCode(userID int64) (string, time.Time, error) {
	code, err := randomCode6()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().Add(s.codeTTL)

	err = s.bindCodeRepo.Upsert(&user.BindCode{
		UserID:    userID,
		Code:      code,
		ExpiresAt: expires,
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return code, expires, nil
}

// IsChannelBound 查询 web 账号是否已绑定指定渠道(feishu/wechat)。
func (s *BindingService) IsChannelBound(userID int64, channelType string) (bool, error) {
	channels, err := s.channelRepo.GetByUserID(userID)
	if err != nil {
		return false, err
	}
	for _, ch := range channels {
		if ch.ChannelType == channelType {
			return true, nil
		}
	}
	return false, nil
}

// BindChannel 渠道端提交绑定码完成绑定(通用,channelType 由调用方传入)。
//
// 校验顺序(PRD 5.2 / 7.3):
//  1. 码存在且未过期,否则 ErrCodeInvalid
//  2. 该 (channelType, open_id) 未绑其他账号,否则 ErrChannelAlreadyBound
//  3. 码对应账号未绑过该渠道,否则 ErrAccountAlreadyBound
//  4. 建 UserChannel;唯一索引冲突兜底并发 -> ErrChannelAlreadyBound
//  5. 成功后删码(幂等:同码再提交 -> ErrCodeInvalid)
func (s *BindingService) BindChannel(channelType, code, openID string) error {
	// 1. 码校验
	stored, err := s.bindCodeRepo.GetByCode(code)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrCodeInvalid
		}
		return err
	}
	if stored.IsExpired(time.Now()) {
		return ErrCodeInvalid
	}

	// 2. (channelType, open_id) 是否已被绑定
	existing, err := s.channelRepo.GetByChannel(channelType, openID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if existing != nil {
		// 该渠道号已绑到某账号;PRD 不允许覆盖,一律拒
		return ErrChannelAlreadyBound
	}

	// 3. 码对应账号是否已绑过该渠道
	bound, err := s.IsChannelBound(stored.UserID, channelType)
	if err != nil {
		return err
	}
	if bound {
		return ErrAccountAlreadyBound
	}

	// 4. 建立绑定
	err = s.channelRepo.Create(user.NewUserChannel(stored.UserID, channelType, openID))
	if err != nil {
		// 并发:另一请求已先绑该 (channelType, open_id) -> 唯一索引冲突
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return ErrChannelAlreadyBound
		}
		return err
	}

	// 5. 删码(幂等)
	_ = s.bindCodeRepo.DeleteByUserID(stored.UserID)
	return nil
}

// ResolveUserID 渠道端身份解析:已绑定返回 (userID, true),未绑定返回 (0, false)。
// 注意:未绑定不建号(v2.2/v2.3 行为变更,PRD 5.4),由上层回引导。
func (s *BindingService) ResolveUserID(channelType, openID string) (int64, bool, error) {
	ch, err := s.channelRepo.GetByChannel(channelType, openID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return ch.UserID, true, nil
}

// randomCode6 用 crypto/rand 生成 6 位数字码(000000~999999,左补零)。
func randomCode6() (string, error) {
	var n [4]byte
	if _, err := rand.Read(n[:]); err != nil {
		return "", err
	}
	// 4 字节 -> uint32,mod 1e6
	var v uint32
	for _, b := range n {
		v = v<<8 | uint32(b)
	}
	v %= 1000000
	return fmt.Sprintf("%06d", v), nil
}
