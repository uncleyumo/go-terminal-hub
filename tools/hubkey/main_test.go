package main

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uncleyumo/go-terminal-hub/remote/relaydb"
)

func TestIssueAndList(t *testing.T) {
	db := freshDB(t)

	issue(db, "akise", 90)

	keys := readKeys(t, db)
	if len(keys) != 1 {
		t.Fatalf("期望 1 张 key，实际 %d 条", len(keys))
	}
	k := keys[0]
	if k.label != "akise" {
		t.Errorf("label = %q，期望 akise", k.label)
	}
	if k.revoked != 0 {
		t.Errorf("新签的 key 不该是吊销状态")
	}
	if k.expiresAt == 0 {
		t.Errorf("days=90 时 expires_at 不该是 0")
	}
	if !strings.HasPrefix(k.id, "key-") {
		t.Errorf("key 要以 key- 开头，实际 %q", k.id)
	}
}

func TestIssueZeroDaysNeverExpires(t *testing.T) {
	db := freshDB(t)
	issue(db, "forever", 0)

	keys := readKeys(t, db)
	if keys[0].expiresAt != 0 {
		t.Errorf("days=0 表示不过期，expires_at 应为 0，实际 %d", keys[0].expiresAt)
	}
	if got := stateText(keys[0].expiresAt, keys[0].revoked, nowUnix()); got != "active" {
		t.Errorf("不过期的 key 状态应为 active，实际 %s", got)
	}
}

func TestIssueUnique(t *testing.T) {
	db := freshDB(t)
	issue(db, "a", 0)
	issue(db, "b", 0)

	keys := readKeys(t, db)
	if keys[0].id == keys[1].id {
		t.Fatal("两次签发拿到了同一个 key，随机源有问题")
	}
}

func TestRevokeAndDelete(t *testing.T) {
	db := freshDB(t)
	issue(db, "temp", 0)
	id := readKeys(t, db)[0].id

	revoke(db, id)
	k := readKeys(t, db)[0]
	if k.revoked != 1 {
		t.Errorf("吊销后 revoked 应为 1，实际 %d", k.revoked)
	}
	if got := stateText(k.expiresAt, k.revoked, nowUnix()); got != "revoked" {
		t.Errorf("吊销后状态应为 revoked，实际 %s", got)
	}

	del(db, id)
	if n := len(readKeys(t, db)); n != 0 {
		t.Errorf("删除后应无记录，实际 %d 条", n)
	}
}

func TestRevokeUnknownKey(t *testing.T) {
	path := freshDB(t)
	if err := revokeErr(mustOpen(t, path), "key-nope"); err == nil {
		t.Error("吊销不存在的 key 应该报错")
	}
}

func TestParseArgsOrder(t *testing.T) {
	// 两种顺序都要能解析出同样的结果：签证时先打 label 再想天数更顺手，
	// 而 flag 包遇到第一个位置参数就停，所以这里是手写解析。
	cases := []struct {
		args     []string
		wantOpt  string
		wantRest string
	}{
		{[]string{"akise", "-days", "90"}, "90", "akise"},
		{[]string{"-days", "90", "akise"}, "90", "akise"},
		{[]string{"akise"}, "", "akise"},
		{[]string{"-days=30", "akise"}, "30", "akise"},
		{[]string{"akise", "-days=0"}, "0", "akise"},
	}
	for _, c := range cases {
		opts, rest := parseArgs(c.args)
		if got := opts["days"]; got != c.wantOpt {
			t.Errorf("parseArgs(%v) days = %q，期望 %q", c.args, got, c.wantOpt)
		}
		if len(rest) != 1 || rest[0] != c.wantRest {
			t.Errorf("parseArgs(%v) rest = %v，期望 [%s]", c.args, rest, c.wantRest)
		}
	}
}

func TestParseArgsPositionalAfterDashDash(t *testing.T) {
	// -- 之后的全是位置参数，哪怕它以 - 开头。
	opts, rest := parseArgs([]string{"--", "-days", "30"})
	if len(opts) != 0 {
		t.Errorf("-- 之后不该有选项，实际 %v", opts)
	}
	if len(rest) != 2 || rest[0] != "-days" || rest[1] != "30" {
		t.Errorf("-- 之后的参数应原样当位置参数，实际 %v", rest)
	}
}

func TestParseArgsMissingValue(t *testing.T) {
	t.Skip("parseArgs 缺值时调 fail() 直接退出进程，测试框架捕获不到，这条留作行为记录")
}

func TestStateText(t *testing.T) {
	now := int64(1_000_000)
	cases := []struct {
		name    string
		expires int64
		revoked int64
		now     int64
		want    string
	}{
		{"正常", now + 30*86400, 0, now, "active"},
		{"已吊销", now + 30*86400, 1, now, "revoked"},
		{"已过期", now - 1, 0, now, "expired"},
		{"7天内过期", now + 3*86400, 0, now, "expiring"},
		{"刚好7天不算即将过期", now + 7*86400, 0, now, "active"},
		{"超过7天", now + 8*86400, 0, now, "active"},
		{"不过期", 0, 0, now, "active"},
		{"吊销优先于过期", now - 1, 1, now, "revoked"},
	}
	for _, c := range cases {
		if got := stateText(c.expires, c.revoked, c.now); got != c.want {
			t.Errorf("%s: stateText(%d,%d,%d) = %s，期望 %s",
				c.name, c.expires, c.revoked, c.now, got, c.want)
		}
	}
}

func TestSchemaIdempotent(t *testing.T) {
	db := freshDB(t)
	// 重复建表不能报错：hubkey 和 relay 都可能抢着初始化。
	// relaydb.Open 内部已经建过一次，这里再显式建一次验证幂等。
	if err := relaydb.EnsureSchema(mustOpen(t, db)); err != nil {
		t.Errorf("重复建表应无害，实际 %v", err)
	}
}

// --- 测试辅助 ---------------------------------------------------------------

type keyRow struct {
	id        string
	label     string
	expiresAt int64
	revoked   int64
}

func readKeys(t *testing.T, dbPath string) []keyRow {
	t.Helper()
	db := mustOpen(t, dbPath)
	defer db.Close()

	rows, err := db.Query("SELECT id, label, expires_at, revoked FROM keys ORDER BY created_at")
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	defer rows.Close()

	var out []keyRow
	for rows.Next() {
		var k keyRow
		if err := rows.Scan(&k.id, &k.label, &k.expiresAt, &k.revoked); err != nil {
			t.Fatalf("扫描失败：%v", err)
		}
		out = append(out, k)
	}
	return out
}

func freshDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "relay.db")
	t.Setenv("HUB_DB", path)
	return path
}

func mustOpen(t *testing.T, path string) *sql.DB {
	t.Helper()
	db := open(path)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func nowUnix() int64 {
	return time.Now().Unix()
}
