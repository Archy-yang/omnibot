// Package larkhome lark-cli 授权状态的 DB 物化(27 号):DB 为唯一事实源,
// 执行 = hydrate → exec → write-back。CLI 状态是黑盒 blob(格式无文档随版本漂移,
// 解析内部结构对 CLI 升级脆弱),只做整目录打包/加密,不解读内容。
package larkhome

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"omnibot/internal/domain/user"
	"omnibot/internal/pkg/crypto"
	userrepo "omnibot/internal/repository/user"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// CLI 状态在 HOME 下的两个落点(2026-10-18 实测,见 27 号 §1.1)。
const (
	relLarkCLI = ".lark-cli"             // profile 配置 + cache
	relXDG     = ".local/share/lark-cli" // token 实体(*.enc)
	homeRoot   = "/tmp/lark-homes"       // per-user 物化根(0700;/tmp 重启自清)
	markerFile = ".lark-state-version"   // 物化版本标记(Hydrate 幂等锚)
)

// 打包排除项:锁文件/日志无状态价值;._* 是 macOS 拷贝产生的 AppleDouble 垃圾。
var excludedNames = map[string]bool{"locks": true, "logs": true}

// Manager blob 的物化与回写。同一用户的调用需外部串行(Executor 负责 per-user 互斥);
// 不同用户互不影响。
type Manager struct {
	repo userrepo.LarkCLIStateRepository
}

func NewManager(repo userrepo.LarkCLIStateRepository) *Manager {
	return &Manager{repo: repo}
}

// HomeDir 用户专属的 CLI 状态物化目录(即执行时注入的 HOME)。
func (m *Manager) HomeDir(userID int64) string {
	return filepath.Join(homeRoot, fmt.Sprintf("user-%d", userID))
}

// Hydrate 把用户的 blob 物化到其 home 目录(幂等:目录标记版本与 DB 一致则跳过)。
// 无 blob(未授权)时只建空目录——执行会自然得到 not_configured,不特殊报错。
func (m *Manager) Hydrate(userID int64) (string, error) {
	home := m.HomeDir(userID)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", fmt.Errorf("建物化目录: %w", err)
	}
	state, err := m.repo.GetByUserID(userID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 无 blob(未授权/已断开):重置为空目录,防陈旧本地状态被残留使用
		if err := os.RemoveAll(home); err != nil {
			return "", fmt.Errorf("清未授权物化目录: %w", err)
		}
		if err := os.MkdirAll(home, 0o700); err != nil {
			return "", err
		}
		return home, nil
	}
	if err != nil {
		return "", fmt.Errorf("读状态 blob: %w", err)
	}
	if marker, err := os.ReadFile(filepath.Join(home, markerFile)); err == nil &&
		strings.TrimSpace(string(marker)) == fmt.Sprint(state.Version) {
		return home, nil // 幂等:同版本已物化
	}
	if err := os.RemoveAll(home); err != nil {
		return "", fmt.Errorf("清旧物化目录: %w", err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	if err := UnpackHome(state.StateBlob, home); err != nil {
		return "", fmt.Errorf("解包状态 blob: %w", err)
	}
	if err := writeMarker(home, state.Version); err != nil {
		return "", err
	}
	return home, nil
}

// writeMarker 记录物化版本(Hydrate 幂等锚;SyncBack 成功后同步推进)。
func writeMarker(home string, version int64) error {
	return os.WriteFile(filepath.Join(home, markerFile), []byte(fmt.Sprint(version)), 0o600)
}

// SyncBack 打包 home 目录回写 DB(版本 CAS,冲突重取重试一次;仍失败记告警不覆盖)。
// 执行中 CLI 会刷新 token,必须回写——否则他机下次 hydrate 拿到已轮转的旧 refresh token。
func (m *Manager) SyncBack(userID int64) error {
	home := m.HomeDir(userID)
	blob, err := PackHome(home)
	if err != nil {
		return fmt.Errorf("打包状态目录: %w", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		state, err := m.repo.GetByUserID(userID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := m.repo.Save(&user.LarkCLIState{UserID: userID, StateBlob: blob, UpdatedAt: time.Now()}); err != nil {
				return err
			}
			return writeMarker(home, 1)
		}
		if err != nil {
			return fmt.Errorf("读状态版本: %w", err)
		}
		ok, err := m.repo.SaveCAS(userID, state.Version, blob)
		if err != nil {
			return err
		}
		if ok {
			return writeMarker(home, state.Version+1)
		}
		logger.WarnWithFields("larkhome: 状态版本 CAS 冲突,重试一次(他机已写)",
			zap.Int64("user_id", userID), zap.Int64("db_version", state.Version))
	}
	// 两次失败:不覆盖胜者。下次 hydrate 拉胜者状态;本机此次的 token 刷新丢失后,
	// CLI 会因凭据失效重新提示授权,不会静默使用坏凭据。
	logger.ErrorWithFields("larkhome: 状态回写两次 CAS 失败,放弃本次覆盖", zap.Int64("user_id", userID))
	return nil
}

// Snapshot 是 SyncBack 的别名(授权动作后的语义化入口)。
func (m *Manager) Snapshot(userID int64) error { return m.SyncBack(userID) }

// ---------- 打包/解包(黑盒 blob;不解读 CLI 内部格式) ----------

// archiveSource 归档内前缀 ↔ HOME 相对路径的映射。
var archiveSources = []struct{ rel, prefix string }{
	{relLarkCLI, "lark-cli"},
	{relXDG, "lark-xdg"},
}

// PackHome 把 home 下两个状态目录打包为加密 blob(tar.gz → AES)。
// 排除 locks/、logs/、._* 与物化标记文件;源目录不存在视为空(未授权/部分缺失)。
func PackHome(home string) ([]byte, error) {
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)

	addFile := func(name string, content []byte, mode os.FileMode) error {
		if mode == 0 {
			mode = 0o600
		}
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: int64(mode), Size: int64(len(content)),
			ModTime: time.Now(),
		}); err != nil {
			return err
		}
		_, err := tw.Write(content)
		return err
	}

	for _, src := range archiveSources {
		root := filepath.Join(home, src.rel)
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				if os.IsNotExist(walkErr) {
					return nil
				}
				return walkErr
			}
			if d.IsDir() {
				if excludedNames[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			relInSrc, err := filepath.Rel(root, path)
			if err != nil || relInSrc == "." || strings.HasPrefix(d.Name(), "._") || d.Name() == markerFile {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return addFile(src.prefix+"/"+filepath.ToSlash(relInSrc), content, d.Type().Perm())
		})
		if err != nil {
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	enc, err := crypto.Encrypt(raw.String())
	if err != nil {
		return nil, fmt.Errorf("加密状态 blob: %w", err)
	}
	return []byte(enc), nil
}

// UnpackHome 解密解包 blob 到 home(反向映射;不清理未涉及的既有文件)。
func UnpackHome(blob []byte, home string) error {
	raw, err := crypto.Decrypt(string(blob))
	if err != nil {
		return fmt.Errorf("解密状态 blob: %w", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader([]byte(raw)))
	if err != nil {
		return fmt.Errorf("gzip 打开: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar 读取: %w", err)
		}
		// 归档名 → HOME 相对路径;未知前缀拒绝(防路径逃逸)
		var rel string
		switch {
		case strings.HasPrefix(hdr.Name, "lark-cli/"):
			rel = filepath.Join(relLarkCLI, strings.TrimPrefix(hdr.Name, "lark-cli/"))
		case strings.HasPrefix(hdr.Name, "lark-xdg/"):
			rel = filepath.Join(relXDG, strings.TrimPrefix(hdr.Name, "lark-xdg/"))
		default:
			return fmt.Errorf("归档含未知条目: %s", hdr.Name)
		}
		if strings.Contains(rel, "..") {
			return fmt.Errorf("归档条目路径可疑: %s", hdr.Name)
		}
		target := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, content, os.FileMode(hdr.Mode)); err != nil {
			return err
		}
	}
}
