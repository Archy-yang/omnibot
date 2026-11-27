package user

// larkcli_auth_repo_test.go — v2.13 授权仓储。
//
// 两件事必须锁住:
//  1. ListByStatus 真的按状态过滤(sweeper 的一切都建立在它上面)
//  2. 新增的 notify_* / expires_at / reissue_count 能完整落库再读回
//     (模型字段加了但 Scan 不到,线上表现是"推不回去",很难查)
//  3. ctx 取消能打断查询——否则 DB 卡住会把 ticker goroutine 钉死

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	domain "omnibot/internal/domain/user"
)

func setupAuthTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&domain.LarkCLIAuth{}))
	return db
}

func seedAuthRow(t *testing.T, repo LarkCLIAuthRepo, userID int64, status string) *domain.LarkCLIAuth {
	t.Helper()
	row := &domain.LarkCLIAuth{
		UserID:       userID,
		ProfileName:  "user-" + itoa(userID),
		AppID:        "cli_x",
		AppSecretEnc: "enc:x",
		Status:       status,
	}
	require.NoError(t, repo.Upsert(row))
	return row
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf []byte
	for v > 0 {
		buf = append([]byte{byte('0' + v%10)}, buf...)
		v /= 10
	}
	return string(buf)
}

// ListByStatus 只返回目标状态的行。
func TestLarkCLIAuthRepo_ListByStatus(t *testing.T) {
	repo := NewLarkCLIAuthRepo(setupAuthTestDB(t))
	seedAuthRow(t, repo, 7, domain.LarkAuthStatusPending)
	seedAuthRow(t, repo, 8, domain.LarkAuthStatusAuthorized)
	seedAuthRow(t, repo, 9, domain.LarkAuthStatusPending)

	rows, err := repo.ListByStatus(context.Background(), domain.LarkAuthStatusPending)
	require.NoError(t, err)
	require.Len(t, rows, 2)

	ids := map[int64]bool{rows[0].UserID: true, rows[1].UserID: true}
	assert.True(t, ids[7] && ids[9], "只应返回 pending 行: %+v", ids)
}

// 新增字段完整往返(落库 → 读回)。
// 这条是 v2.13 的隐性依赖:notify 字段丢了 → 收尾后推不回用户;expires_at 丢了 → 永不过期。
func TestLarkCLIAuthRepo_NotifyAndExpiryFieldsRoundTrip(t *testing.T) {
	repo := NewLarkCLIAuthRepo(setupAuthTestDB(t))
	expires := time.Now().Add(10 * time.Minute).Truncate(time.Second)

	row := seedAuthRow(t, repo, 7, domain.LarkAuthStatusPending)
	row.NotifyChannel = "feishu"
	row.NotifyTarget = "ou_peer"
	row.ExpiresAt = expires
	row.ReissueCount = 1
	row.DeviceCode = "dev-1"
	row.VerifiedURL = "https://verify/x?user_code=CODE"
	require.NoError(t, repo.Update(row))

	got, err := repo.GetByUserID(7)
	require.NoError(t, err)
	assert.Equal(t, "feishu", got.NotifyChannel)
	assert.Equal(t, "ou_peer", got.NotifyTarget)
	assert.Equal(t, 1, got.ReissueCount)
	assert.WithinDuration(t, expires, got.ExpiresAt, time.Second)
}

// ctx 取消必须能打断查询(ticker 不被 DB 拖死的底层保障)。
func TestLarkCLIAuthRepo_ListByStatusHonorsContext(t *testing.T) {
	repo := NewLarkCLIAuthRepo(setupAuthTestDB(t))
	seedAuthRow(t, repo, 7, domain.LarkAuthStatusPending)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := repo.ListByStatus(ctx, domain.LarkAuthStatusPending)
	require.Error(t, err, "取消的 ctx 必须让查询失败,而不是静默返回")
}

// Update 是整行覆盖:收尾清空 device_code 后,notify 绑定不能被顺手抹掉。
func TestLarkCLIAuthRepo_UpdateKeepsNotifyBinding(t *testing.T) {
	repo := NewLarkCLIAuthRepo(setupAuthTestDB(t))
	row := seedAuthRow(t, repo, 7, domain.LarkAuthStatusPending)
	row.NotifyChannel, row.NotifyTarget = "wechat_claw", "peer-9"
	require.NoError(t, repo.Update(row))

	// 收尾成功:清 device_code/url/expires
	row.Status = domain.LarkAuthStatusAuthorized
	row.DeviceCode, row.VerifiedURL = "", ""
	row.ExpiresAt = time.Time{}
	require.NoError(t, repo.Update(row))

	got, err := repo.GetByUserID(7)
	require.NoError(t, err)
	assert.Equal(t, domain.LarkAuthStatusAuthorized, got.Status)
	assert.Equal(t, "wechat_claw", got.NotifyChannel, "收尾后仍要记得该推到哪个渠道")
	assert.Equal(t, "peer-9", got.NotifyTarget)
}

// 首次落库走 Create 分支;再次 Upsert 走覆盖分支。
func TestLarkCLIAuthRepo_UpsertCreateThenUpdate(t *testing.T) {
	repo := NewLarkCLIAuthRepo(setupAuthTestDB(t))

	first := &domain.LarkCLIAuth{
		UserID: 7, ProfileName: "user-7", AppID: "cli_old", AppSecretEnc: "enc:x",
		Status: domain.LarkAuthStatusPending,
	}
	require.NoError(t, repo.Upsert(first))

	second := &domain.LarkCLIAuth{
		UserID: 7, ProfileName: "user-7", AppID: "cli_new", AppSecretEnc: "enc:y",
		Status: domain.LarkAuthStatusNeedRelogin,
	}
	require.NoError(t, repo.Upsert(second))

	got, err := repo.GetByUserID(7)
	require.NoError(t, err)
	assert.Equal(t, "cli_new", got.AppID, "Upsert 应覆盖同一 user_id 的行")
	assert.Equal(t, domain.LarkAuthStatusNeedRelogin, got.Status)
}

func TestLarkCLIAuthRepo_GetMissing(t *testing.T) {
	repo := NewLarkCLIAuthRepo(setupAuthTestDB(t))
	_, err := repo.GetByUserID(7)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound, "未授权应是 ErrRecordNotFound(上层据此判 none)")
}

// Disconnect 走 Delete:整行消失,profile 名不再被解析出来。
func TestLarkCLIAuthRepo_Delete(t *testing.T) {
	repo := NewLarkCLIAuthRepo(setupAuthTestDB(t))
	seedAuthRow(t, repo, 7, domain.LarkAuthStatusAuthorized)

	require.NoError(t, repo.Delete(7))
	_, err := repo.GetByUserID(7)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}
