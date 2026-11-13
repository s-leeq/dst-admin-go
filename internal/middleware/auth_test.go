package middleware

import "testing"

// /backup/restore 修复前因为「路径不含 /api」被当成公开接口，
// 既跳过登录也跳过 ClusterMiddleware，这里锁定修复后的行为。
func TestAPIFilter(t *testing.T) {
	whitelist := []string{"/api/login", "/api/logout", "/api/init", "/api/install/steamcmd"}

	cases := []struct {
		path string
		want bool
	}{
		{"/api/login", true},
		{"/api/init", true},
		{"/api/game/backup", false},
		{"/api/game/backup/restore", false},
		{"/backup/restore", false}, // 关键回归点
		{"/", false},
		{"/assets/index.js", false},
	}

	for _, c := range cases {
		if got := apiFilter(whitelist, c.path); got != c.want {
			t.Errorf("apiFilter(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
