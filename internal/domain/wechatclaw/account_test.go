package wechatclaw

import "testing"

func TestAccountIsActive(t *testing.T) {
	active := &Account{Status: StatusActive}
	if !active.IsActive() {
		t.Fatal("active 账号应为可用")
	}

	expired := &Account{Status: StatusExpired}
	if expired.IsActive() {
		t.Fatal("expired 账号应为不可用")
	}
}

func TestAccountTableName(t *testing.T) {
	if got := (Account{}).TableName(); got != "wechat_claw_accounts" {
		t.Fatalf("表名不符: %s", got)
	}
}
