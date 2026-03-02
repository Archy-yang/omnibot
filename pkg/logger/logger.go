package logger

import (
	"os"

	"wechat-intelligent-bot/pkg/config"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var log *zap.Logger

// Init 初始化日志
func Init(cfg config.LoggerConfig) {
	// 设置日志级别
	level := zapcore.InfoLevel
	if err := level.Set(cfg.Level); err != nil {
		// 默认使用info级别
		level = zapcore.InfoLevel
	}

	// 创建编码器
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	encoderConfig.EncodeCaller = zapcore.ShortCallerEncoder

	var encoder zapcore.Encoder
	if cfg.Format == "console" {
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	} else {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	}

	// 创建写入器
	var writer zapcore.WriteSyncer
	if cfg.Output == "stdout" {
		writer = zapcore.AddSync(os.Stdout)
	} else {
		// 使用Lumberjack进行日志滚动
		lumberjackLogger := &lumberjack.Logger{
			Filename:   cfg.Output,
			MaxSize:    100, // 100MB
			MaxBackups: 7,   // 保留7个备份
			MaxAge:     28,  // 保留28天
			Compress:   true,
		}
		writer = zapcore.AddSync(lumberjackLogger)
	}

	// 创建Core
	core := zapcore.NewCore(encoder, writer, level)

	// 创建Logger
	log = zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))

	// 替换全局Logger
	zap.ReplaceGlobals(log)
}

// Debug 调试日志
func Debug(args ...interface{}) {
	log.Sugar().Debug(args...)
}

// DebugWithFields 带字段的调试日志
func DebugWithFields(msg string, fields ...zap.Field) {
	log.Debug(msg, fields...)
}

// Info 信息日志
func Info(args ...interface{}) {
	log.Sugar().Info(args...)
}

// InfoWithFields 带字段的信息日志
func InfoWithFields(msg string, fields ...zap.Field) {
	log.Info(msg, fields...)
}

// Warn 警告日志
func Warn(args ...interface{}) {
	log.Sugar().Warn(args...)
}

// WarnWithFields 带字段的警告日志
func WarnWithFields(msg string, fields ...zap.Field) {
	log.Warn(msg, fields...)
}

// Error 错误日志
func Error(args ...interface{}) {
	log.Sugar().Error(args...)
}

// ErrorWithFields 带字段的错误日志
func ErrorWithFields(msg string, fields ...zap.Field) {
	log.Error(msg, fields...)
}

// Fatal 致命错误日志
func Fatal(args ...interface{}) {
	log.Sugar().Fatal(args...)
}

// FatalWithFields 带字段的致命错误日志
func FatalWithFields(msg string, fields ...zap.Field) {
	log.Fatal(msg, fields...)
}
