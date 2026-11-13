package archive

import (
	"testing"

	"dst-admin-go/internal/service/dstConfig"
)

type fakeDstConfig struct {
	cfg dstConfig.DstConfig
}

func (f fakeDstConfig) GetDstConfig(string) (dstConfig.DstConfig, error) { return f.cfg, nil }
func (f fakeDstConfig) SaveDstConfig(string, dstConfig.DstConfig) error  { return nil }

func newResolver(t *testing.T) *PathResolver {
	t.Helper()
	r, err := NewPathResolver(fakeDstConfig{cfg: dstConfig.DstConfig{
		Persistent_storage_root: "/app/data",
	}})
	if err != nil {
		t.Fatalf("NewPathResolver: %v", err)
	}
	return r
}

// 空 cluster 会让 ClusterPath 退化成克雷根目录，调用方一旦删除就会清掉所有集群
func TestClusterPathPanicsOnEmptyCluster(t *testing.T) {
	r := newResolver(t)
	defer func() {
		if recover() == nil {
			t.Fatal("ClusterPath(\"\") 应该 panic，而不是返回根目录")
		}
	}()
	r.ClusterPath("")
}

func TestClusterPathJoinsClusterName(t *testing.T) {
	r := newResolver(t)
	got := r.ClusterPath("Cluster_1")
	want := "/app/data/DoNotStarveTogether/Cluster_1"
	if got != want {
		t.Fatalf("ClusterPath = %q, want %q", got, want)
	}
}
