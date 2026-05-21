// Package frontend 提供嵌入的前端静态资源
package frontend

import "embed"

// FS 嵌入的前端静态资源文件系统
//
//go:embed public/*
var FS embed.FS
