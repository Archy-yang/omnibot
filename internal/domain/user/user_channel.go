package user

import (
	"encoding/json"
	"time"
)

// UserChannel 用户通道关联
// 一个 User 可以关联多个不同通道的身份
type UserChannel struct {
	ID            int64           `gorm:"primaryKey;autoIncrement"`
	UserID        int64         `gorm:"not null;index:idx_user_channel_user_id"`
	ChannelType   string        `gorm:"size:32;not null;index:idx_user_channel_type"` // "wechat", "feishu", "web"
	ChannelUserID string        `gorm:"size:128;not null;column:channel_user_id"`  // 通道内的用户 ID（OpenID、飞书 UserID 等）
	ChannelRawData *ChannelData `gorm:"type:json;serializer:json"` // 通道特定的额外数据

	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	User User `gorm:"foreignKey:UserID"`
}

// TableName 返回表名
func (UserChannel) TableName() string {
	return "user_channels"
}

// ChannelData 通道特定数据
type ChannelData struct {
	// 微信专用字段
	UnionID *string `json:"union_id,omitempty"`
	AppID   string  `json:"app_id,omitempty"`

	// 其他字段可以后续扩展
	Extra map[string]interface{} `json:"extra,omitempty"`
}

// Scan 实现 gorm.Scanner 接口用于 JSON 反序列化
func (c *ChannelData) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(bytes, c)
}

// NewUserChannel 创建新的用户通道关联
func NewUserChannel(userID int64, channelType string, channelUserID string) *UserChannel {
	now := time.Now()
	return &UserChannel{
		UserID:         userID,
		ChannelType:    channelType,
		ChannelUserID:  channelUserID,
		ChannelRawData: &ChannelData{},
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

// SetUnionID 设置微信 UnionID
func (uc *UserChannel) SetUnionID(unionID string) {
	if uc.ChannelRawData == nil {
		uc.ChannelRawData = &ChannelData{}
	}
	uc.ChannelRawData.UnionID = &unionID
	uc.UpdatedAt = time.Now()
}

// SetAppID 设置微信 AppID
func (uc *UserChannel) SetAppID(appID string) {
	if uc.ChannelRawData == nil {
		uc.ChannelRawData = &ChannelData{}
	}
	uc.ChannelRawData.AppID = appID
	uc.UpdatedAt = time.Now()
}
