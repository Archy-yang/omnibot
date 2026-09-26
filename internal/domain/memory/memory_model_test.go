package memory

import (
	"testing"
)

// TestNewMemory_ManualSource 显式记忆构造:Source 默认 manual,无溯源指针(12-记忆系统技术方案 §4.1)。
func TestNewMemory_ManualSource(t *testing.T) {
	m := NewMemory(42, "用户偏好简洁回复")

	if m.UserID != 42 {
		t.Errorf("UserID = %d, want 42", m.UserID)
	}
	if m.Content != "用户偏好简洁回复" {
		t.Errorf("Content = %q", m.Content)
	}
	if m.Source != MemorySourceManual {
		t.Errorf("Source = %q, want %q (显式 #记住 默认 manual)", m.Source, MemorySourceManual)
	}
	if m.SourceMessageID != nil {
		t.Errorf("SourceMessageID should be nil for manual, got %v", *m.SourceMessageID)
	}
	if m.Embedding != nil {
		t.Error("Embedding should be nil at construction (由 service 层嵌入后 stamp)")
	}
	if m.EmbeddingModel != "" {
		t.Errorf("EmbeddingModel should be empty at construction, got %q", m.EmbeddingModel)
	}
	if m.CreatedAt.IsZero() || m.UpdatedAt.IsZero() {
		t.Error("CreatedAt/UpdatedAt should be set")
	}
}

// TestNewAutoMemory 自动沉淀记忆构造:Source=auto + 溯源指针(PRD AC1.3/技术方案 §4.1)。
func TestNewAutoMemory(t *testing.T) {
	srcID := int64(1001)
	m := NewAutoMemory(42, "用户是后端工程师", &srcID)

	if m.Source != MemorySourceAuto {
		t.Errorf("Source = %q, want %q", m.Source, MemorySourceAuto)
	}
	if m.SourceMessageID == nil || *m.SourceMessageID != srcID {
		t.Errorf("SourceMessageID = %v, want %d", m.SourceMessageID, srcID)
	}
	if m.UserID != 42 || m.Content != "用户是后端工程师" {
		t.Errorf("UserID/Content = %d/%q, want 42/用户是后端工程师", m.UserID, m.Content)
	}
}
