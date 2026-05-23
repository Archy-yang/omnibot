package memory

import "time"

type Memory struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	UserID    int64     `gorm:"index;not null"`
	Content   string    `gorm:"type:text;not null"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

func (Memory) TableName() string {
	return "memories"
}

func NewMemory(userID int64, content string) *Memory {
	now := time.Now()
	return &Memory{UserID: userID, Content: content, CreatedAt: now, UpdatedAt: now}
}
