package larkhome

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	domain "omnibot/internal/domain/user"
	"omnibot/internal/pkg/crypto"
	userrepo "omnibot/internal/repository/user"
)

func setupManager(t *testing.T) (*Manager, userrepo.LarkCLIStateRepository) {
	t.Helper()
	crypto.InitWithKey([]byte("01234567890123456789012345678901"))
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&domain.LarkCLIState{}))
	repo := userrepo.NewLarkCLIStateRepository(db)
	return NewManager(repo), repo
}

// seedHome 造一个带全部目录类别的假 CLI 状态目录。
func seedHome(t *testing.T, home string) {
	t.Helper()
	write := func(rel, content string) {
		p := filepath.Join(home, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	}
	write(filepath.Join(relLarkCLI, "config.json"), `{"apps":1}`)
	write(filepath.Join(relLarkCLI, "cache", "auth_login_scopes", "token.json"), `{"token":"t"}`)
	write(filepath.Join(relLarkCLI, "locks", "x.lock"), "junk")        // 应排除
	write(filepath.Join(relLarkCLI, "logs", "auth.log"), "junk")       // 应排除
	write(filepath.Join(relLarkCLI, "._config.json"), "junk")          // 应排除(AppleDouble)
	write(filepath.Join(relXDG, "tat_v1_abc.enc"), "token-blob")
}

func TestPackUnpack_RoundTripWithExclusions(t *testing.T) {
	crypto.InitWithKey([]byte("01234567890123456789012345678901"))
	home := t.TempDir()
	seedHome(t, home)

	blob, err := PackHome(home)
	require.NoError(t, err)
	require.NotEmpty(t, blob)

	restored := t.TempDir()
	require.NoError(t, UnpackHome(blob, restored))

	// 应保留的文件内容一致
	for _, rel := range []string{
		filepath.Join(relLarkCLI, "config.json"),
		filepath.Join(relLarkCLI, "cache", "auth_login_scopes", "token.json"),
		filepath.Join(relXDG, "tat_v1_abc.enc"),
	} {
		want, err := os.ReadFile(filepath.Join(home, rel))
		require.NoError(t, err)
		got, err := os.ReadFile(filepath.Join(restored, rel))
		require.NoError(t, err, "缺失: %s", rel)
		assert.Equal(t, want, got, "内容不一致: %s", rel)
	}
	// 应排除的路径不存在
	for _, rel := range []string{
		filepath.Join(relLarkCLI, "locks"),
		filepath.Join(relLarkCLI, "logs"),
		filepath.Join(relLarkCLI, "._config.json"),
	} {
		_, err := os.Stat(filepath.Join(restored, rel))
		assert.True(t, os.IsNotExist(err), "应被排除: %s", rel)
	}
}

func TestManager_HydrateSyncBack_RoundTrip(t *testing.T) {
	mgr, _ := setupManager(t)
	userID := int64(7)

	// 无 blob:Hydrate 给空目录(未授权用户不炸)
	home, err := mgr.Hydrate(userID)
	require.NoError(t, err)
	assert.DirExists(t, home)

	// 造目录状态 → SyncBack 落 DB
	seedHome(t, home)
	require.NoError(t, mgr.SyncBack(userID))

	// 清掉本地目录 → Hydrate 恢复
	require.NoError(t, os.RemoveAll(home))
	home2, err := mgr.Hydrate(userID)
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(home2, relLarkCLI, "config.json"))
	require.NoError(t, err)
	assert.Equal(t, `{"apps":1}`, string(got))
}

func TestManager_Hydrate_Idempotent(t *testing.T) {
	mgr, _ := setupManager(t)
	userID := int64(7)

	home, err := mgr.Hydrate(userID)
	require.NoError(t, err)
	seedHome(t, home)
	require.NoError(t, mgr.SyncBack(userID))

	// 同版本重复 Hydrate:不重复解包(幂等),目录内容不受影响
	require.NoError(t, os.WriteFile(filepath.Join(home, relLarkCLI, "config.json"), []byte(`{"apps":2}`), 0o600))
	_, err = mgr.Hydrate(userID)
	require.NoError(t, err)
	got, _ := os.ReadFile(filepath.Join(home, relLarkCLI, "config.json"))
	assert.Equal(t, `{"apps":2}`, string(got), "同版本 Hydrate 不应覆盖本地目录")
}

func TestManager_SyncBack_CASConflictLogged(t *testing.T) {
	mgr, repo := setupManager(t)
	userID := int64(7)

	home, err := mgr.Hydrate(userID)
	require.NoError(t, err)
	seedHome(t, home)

	// 模拟他机抢写:版本被推高
	require.NoError(t, repo.Save(&domain.LarkCLIState{UserID: userID, StateBlob: []byte("other-machine"), Version: 5}))
	current, _ := repo.GetByUserID(userID)

	// 本地 SyncBack:第一次 CAS 因版本过老失败,重取重试一次后成功
	require.NoError(t, mgr.SyncBack(userID))
	got, err := repo.GetByUserID(userID)
	require.NoError(t, err)
	assert.NotEqual(t, "other-machine", string(got.StateBlob), "重试后本机状态应胜出")
	assert.Greater(t, got.Version, current.Version)
}
