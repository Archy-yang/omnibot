# 架构说明：pkg/config/config.go

## 模块职责
加载配置文件，解析到配置结构体。

## 入口函数
`Load(configPath string) (*Config, error)`

## 配置结构体

### 顶层
```go
type Config struct {
    App    AppConfig
    Wechat WechatConfig
    LLM    LLMConfig
    Memory MemoryConfig
    Redis  RedisConfig
    Logger LoggerConfig
}
```

### AppConfig - 应用配置
| 字段 | 类型 | 说明 |
|------|------|------|
| Name | string | 应用名称 |
| Env | string | 运行环境 (development/production) |
| Port | int | 监听端口 |

### WechatConfig - 微信配置
| 字段 | 类型 | 说明 |
|------|------|------|
| AppID | string | 公众号 AppID |
| AppSecret | string | 公众号 AppSecret |
| Token | string | 消息校验 Token |
| EncodingAESKey | string | 消息加密 Key |
| CallbackURL | string | 回调地址 |

### LLMConfig - 大模型配置
| 字段 | 类型 | 说明 |
|------|------|------|
| Providers | map[string]ProviderConfig | 多提供商配置 |
| Routing | LMRoutingConfig | 路由配置 |

### MemoryConfig - 记忆系统配置
| 字段 | 类型 | 说明 |
|------|------|------|
| Extraction | ExtractionConfig | 抽取配置 |
| Storage | StorageConfig | 存储配置 |

### 其他
- RedisConfig - Redis 连接配置
- LoggerConfig - 日志配置 (Level, Format, Output)

## 加载流程
```
1. 如果未指定 configPath → 使用默认路径 configs/config.yaml
   ↓
2. 如果默认文件不存在 → 回退到 configs/config.example.yaml
   ↓
3. Viper 读取配置文件
   ↓
4. 自动绑定环境变量 (前缀 WECHAT_BOT_)
   ↓
5. 反序列化到 Config 结构体
   ↓
6. 返回配置指针
```

## 已实现能力
- ✅ 基于 Viper 配置管理
- ✅ 支持 YAML 格式
- ✅ 环境变量覆盖配置文件
- ✅ 默认配置文件自动回退
- ✅ 完整的配置结构体定义，覆盖所有规划模块

## 依赖
- `github.com/spf13/viper`
