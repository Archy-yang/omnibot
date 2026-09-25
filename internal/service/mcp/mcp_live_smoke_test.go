package mcp

// mcp_live_smoke_test.go — go-sdk 迁移 live smoke:直连本地 PG 取高德连接器配置,
// 解密 key 后走 go-sdk 工具发现全链路。LIVE_SMOKE=1 时才跑(go test ./... 不打外网)。
// 跑法: LIVE_SMOKE=1 go test ./internal/service/mcp/ -run TestLiveMCPSmoke -v

import (
	"context"
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/stretchr/testify/require"
)

// openSmokeDB 直连本地开发库(与 configs/config.yaml 同 DSN)。
func openSmokeDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("SMOKE_DB_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5433 user=test password=test123 dbname=omnibot_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	return db
}

func TestLiveMCPSmoke(t *testing.T) {
	if os.Getenv("LIVE_SMOKE") == "" {
		t.Skip("需要 LIVE_SMOKE=1(打真站/真库)")
	}
	// 复用真库:取高德连接器(生产同款解密路径,服务未设加密 key = 默认 key)
	db := openSmokeDB(t)
	rows, err := db.Raw(`SELECT name, base_url, api_key, auth_type, transport FROM mcp_servers WHERE enabled = true`).Rows()
	require.NoError(t, err)
	defer rows.Close()

	var specs []MCPServerSpec
	for rows.Next() {
		var name, baseURL, apiKey, authType, transport string
		require.NoError(t, rows.Scan(&name, &baseURL, &apiKey, &authType, &transport))
		key, err := decryptSecret(apiKey)
		require.NoError(t, err, "存量密钥应可用默认 key 解密")
		specs = append(specs, MCPServerSpec{Name: name, BaseURL: baseURL, APIKey: key, AuthType: authType, Transport: transport, Enabled: true})
	}
	require.NotEmpty(t, specs, "库中应有启用的连接器")

	for _, spec := range specs {
		t.Run(spec.Name, func(t *testing.T) {
			cli, err := NewStreamableHTTPMCPClient(context.Background(), spec)
			require.NoError(t, err, "go-sdk 连接+握手应成功")
			defer func() { _ = cli.Close() }()

			tools, err := cli.ListTools(context.Background())
			require.NoError(t, err)
			t.Logf("server %q 发现 %d 个工具,前 3 个:", spec.Name, len(tools))
			for i, tool := range tools {
				if i >= 3 {
					break
				}
				t.Logf("  【%s】%s", tool.Name, tool.Description)
			}
			require.NotEmpty(t, tools)
		})
	}
}
