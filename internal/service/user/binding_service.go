// Package user 的 BindingService 实现飞书账号绑定(v2.2)。
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

// 飞书绑定相关 sentinel errors,handler 层用 errors.Is 映射用户提示(PRD 5.2)
var (
	// ErrCodeInvalid 绑定码不存在或已过期
	ErrCodeInvalid = errors.New("bind code invalid or expired")
	// ErrFeishuAlreadyBound 该飞书号已绑定其他账号
	ErrFeishuAlreadyBound = errors.New("feishu account already bound")
	// ErrAccountAlreadyBound 该 web 账号已绑定过飞书号
	ErrAccountAlreadyBound = errors.New("account already bound feishu")
)

// 飞书渠道类型常量(与 channel/feishu 包约定一致)
const feishuChannelType = "feishu"

// BindingService 飞书账号绑定服务。
//
// 绑定关系最终落到 user_channels(feishu, open_id, user_id) 一行;
// v2.1 已为 (channel_type, channel_user_id) 建复合唯一索引,天然防「一号多账号」。
// 绑定码用 feishu_bind_codes 表持久化(5 分钟过期,upsert 作废旧码)。
type BindingService struct {
	channelRepo  UserChannelRepository
	bindCodeRepo userrepo.FeishuBindCodeRepository
	codeTTL      time.Duration
}

// NewBindingService 创建 BindingService。
// codeTTL 建议 5 分钟(PRD 4.1)。
func NewBindingService(
	channelRepo UserChannelRepository,
	bindCodeRepo userrepo.FeishuBindCodeRepository,
	codeTTL time.Duration,
) *BindingService {
	return &BindingService{
		channelRepo:  channelRepo,
		bindCodeRepo: bindCodeRepo,
		codeTTL:      codeTTL,
	}
}

// GenerateCode 为 web 账号生成 6 位绑定码。
// 若账号已绑飞书 -> ErrAccountAlreadyBound(PRD 5.1:已绑不出码)。
// 重新生成会作废旧码(repo upsert 按 user_id 覆盖)。
func (s *BindingService) GenerateCode(userID int64) (string, time.Time, error) {
	bound, err := s.IsFeishuBound(userID)
	if err != nil {
		return "", time.Time{}, err
	}
	if bound {
		return "", time.Time{}, ErrAccountAlreadyBound
	}

	code, err := randomCode6()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().Add(s.codeTTL)

	err = s.bindCodeRepo.Upsert(&user.FeishuBindCode{
		UserID:    userID,
		Code:      code,
		ExpiresAt: expires,
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return code, expires, nil
}

// IsFeishuBound 查询 web 账号是否已绑定飞书号。
func (s *BindingService) IsFeishuBound(userID int64) (bool, error) {
	channels, err := s.channelRepo.GetByUserID(userID)
	if err != nil {
		return false, err
	}
	for _, ch := range channels {
		if ch.ChannelType == feishuChannelType {
			return true, nil
		}
	}
	return false, nil
}

// BindFeishu 飞书端提交绑定码完成绑定。
//
// 校验顺序(PRD 5.2 / 7.3):
//  1. 码存在且未过期,否则 ErrCodeInvalid
//  2. 该 open_id 未绑其他账号,否则 ErrFeishuAlreadyBound
//  3. 码对应账号未绑过飞书,否则 ErrAccountAlreadyBound
//  4. 建 UserChannel;唯一索引冲突兜底并发 -> ErrFeishuAlreadyBound
//  5. 成功后删码(幂等:同码再提交 -> ErrCodeInvalid)
func (s *BindingService) BindFeishu(code, openID string) error {
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

	// 2. open_id 是否已被绑定
	existing, err := s.channelRepo.GetByChannel(feishuChannelType, openID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if existing != nil {
		// 已绑到某账号(可能是同一个,也可能不同);PRD 不允许覆盖,一律拒
		return ErrFeishuAlreadyBound
	}

	// 3. 码对应账号是否已绑过飞书
	bound, err := s.IsFeishuBound(stored.UserID)
	if err != nil {
		return err
	}
	if bound {
		return ErrAccountAlreadyBound
	}

	// 4. 建立绑定
	err = s.channelRepo.Create(user.NewUserChannel(stored.UserID, feishuChannelType, openID))
	if err != nil {
		// 并发:另一请求已先绑该 open_id -> 唯一索引冲突
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return ErrFeishuAlreadyBound
		}
		return err
	}

	// 5. 删码(幂等)
	_ = s.bindCodeRepo.DeleteByUserID(stored.UserID)
	return nil
}

// ResolveFeishuUserID 飞书端身份解析:已绑定返回 (userID, true),未绑定返回 (0, false)。
// 注意:未绑定不建号(v2.2 行为变更,PRD 5.4),由上层回引导。
func (s *BindingService) ResolveFeishuUserID(openID string) (int64, bool, error) {
	ch, err := s.channelRepo.GetByChannel(feishuChannelType, openID)
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
