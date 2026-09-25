package user

import "errors"

// ErrEmailAlreadyExists 邮箱已被注册(channel_type+channel_user_id 唯一冲突)。
// 定义在 domain 层:repository 实现(事务内唯一冲突转译)与 service 层共用,
// 避免 repository → service 的反向依赖。
var ErrEmailAlreadyExists = errors.New("email already exists")
