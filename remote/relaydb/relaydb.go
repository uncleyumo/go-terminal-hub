// Package relaydb 是 relay 和 hubkey 之间唯一的约定。
//
// 这里只有两样东西：
//   - 数据库文件的默认位置
//   - 表结构（建表语句）
//
// 为什么必须抽成共用包：relay 靠 systemd 的 EnvironmentFile 知道数据库在哪，
// hubkey 靠命令行参数知道。两边各写一份默认路径的话，改一处漏一处，
// 症状是「签证成功了但连不上」——最难查的那类，因为两边都不报错。
//
// ⚠️ 改这里的表结构就必须同步改 hubkey 的 ensureSchema，两边不一致的后果
// 是一方 INSERT 的字段另一方不认识，启动即失败。
package relaydb

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite" // 纯 Go 驱动，不需要 cgo
)

// DefaultPath 是数据库文件的默认位置。
//
// /var/lib/hub-relay/relay.db 而不是 /var/lib/hub-relay.db：
// systemd 的 ProtectSystem=full 只放行 /var 以下，但 /var/lib 本身是 root
// 拥有的——服务用户建不了 WAL 文件（SQLite 的 -wal 和 -shm 要建在同目录）。
// 放进 /var/lib/hub-relay/ 这个服务用户拥有的子目录就没有这个问题。
//
// 这个值同时是 relay 和 hubkey 的默认值，改的话两边一起变。
const DefaultPath = "/var/lib/hub-relay/relay.db"

// Schema 是全部表结构。用 IF NOT EXISTS，谁先跑谁建，重复执行无害。
//
// 这个常量被 hubkey 和 relay 共用。表结构只在启动时校验一次，
// 之后两边都假设它是这个形状。
//
// secrets 存的是登陆密钥的 SHA-256，不是明文：密钥只在 register_secret
// 那条消息里明文出现一次，库里留的是哈希。
//
// 一个 key 只对应一个登陆密钥（PRIMARY KEY on key_id）：桌面端轮换密钥时
// 直接覆盖，旧密钥当场失效。用 ON CONFLICT DO UPDATE 而不是先删再插，
// 是为了「换密钥」和「首次登记」走同一条语句。
const Schema = `
CREATE TABLE IF NOT EXISTS keys (
	id          TEXT PRIMARY KEY,
	label       TEXT NOT NULL,
	created_at  INTEGER NOT NULL,
	expires_at  INTEGER NOT NULL,
	revoked     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS secrets (
	key_id      TEXT PRIMARY KEY REFERENCES keys(id) ON DELETE CASCADE,
	secret_hash TEXT NOT NULL,
	created_at  INTEGER NOT NULL
)`

// Open 打开数据库并确保表结构存在。
//
// 三个参数是刻意的：
//
//   - busy_timeout：relay 可能正持着写锁。没这个超时，hubkey 一上来就是
//     SQLITE_BUSY 直接失败，看起来像「签证用不了」。
//   - journal_mode(WAL)：读写不互斥。hubkey 签证时 relay 还能正常读，
//     不会因为一次 50 毫秒的插入把在用的连接卡住。
//   - foreign_keys：SQLite 默认**关**着外键约束。不显式开的话，
//     secrets 表上的 ON DELETE CASCADE 不会生效——删 key 时密钥留成孤儿。
//   - SetMaxOpenConns(1)：SQLite 同一时刻只接受一个写者，database/sql
//     默认会开多个连接，并发写会互相锁。relay 和 hubkey 都是单操作模型，
//     限死成一个正合适。
func Open(path string) (*sql.DB, error) {
	if path == "" {
		path = DefaultPath
	}

	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	if err := EnsureSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// EnsureSchema 建表。幂等，重复调用无害。
//
// 分句执行而不是一个 Exec 塞多条：SQLite 的驱动对多语句 Exec 的支持
// 不稳定，而且一条失败时分不出是哪条——分句跑，错了能指出是哪张表。
func EnsureSchema(db *sql.DB) error {
	for _, stmt := range strings.Split(Schema, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("%s: %w", firstLine(stmt), err)
		}
	}
	return nil
}

// firstLine 取语句的第一行，做错误信息里的定位用。
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
