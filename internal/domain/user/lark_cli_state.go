package user

import "time"

// LarkCLIState lark-cli 授权状态的 DB 物化(27 号):blob 是 CLI 状态目录的黑盒快照
// (profile 配置 + XDG token 实体,排除 locks/logs,打包后 AES 加密)。
// DB 为唯一事实源;执行 = hydrate → exec → write-back(执行中会刷新 token,必须回写)。
type LarkCLIState struct {
	UserID    int64     `gorm:"primaryKey"`          // 与 user_larkcli_auths 1:1
	StateBlob []byte    `gorm:"type:bytes;not null"` // 打包+加密后的快照
	Version   int64     `gorm:"not null;default:1"`  // 每次写回 +1;多机 CAS 锚
	UpdatedAt time.Time `gorm:"not null"`
}

func (LarkCLIState) TableName() string {
	return "lark_cli_states"
}
