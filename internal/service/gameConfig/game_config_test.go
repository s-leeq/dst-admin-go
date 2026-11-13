package gameConfig

import (
	"testing"

	"dst-admin-go/internal/service/archive"
	"dst-admin-go/internal/service/dstConfig"
)

type fakeDstConfig struct{ cfg dstConfig.DstConfig }

func (f fakeDstConfig) GetDstConfig(string) (dstConfig.DstConfig, error) { return f.cfg, nil }
func (f fakeDstConfig) SaveDstConfig(string, dstConfig.DstConfig) error  { return nil }

// token 未配置时应当返回空字符串而不是错误；
// 否则 GET /api/game/8level/clusterIni 会 500，前端房间设置页无法提交表单。
func TestGetClusterTokenMissingFile(t *testing.T) {
	r, err := archive.NewPathResolver(fakeDstConfig{cfg: dstConfig.DstConfig{
		Persistent_storage_root: t.TempDir(),
	}})
	if err != nil {
		t.Fatalf("NewPathResolver: %v", err)
	}
	g := NewGameConfig(r, nil)

	token, err := g.GetClusterToken("Cluster_1")
	if err != nil {
		t.Fatalf("缺少 token 文件时不应返回错误，got %v", err)
	}
	if token != "" {
		t.Fatalf("token 应为空字符串，got %q", token)
	}
}
