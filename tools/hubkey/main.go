// hubkey 管理「使用 key」——桌面端连中央站用的凭证。
//
// 为什么要有它：中央站是个多租户的转发器，每套 key 一个槽位。key 就是那套槽位的
// 门牌号，得有个地方签发、查看、吊销。放在这里而不是做成 relay 的一个子命令，
// 是因为它不该有网络入口：这个程序只连本机的 SQLite 文件，不监听端口，
// 能跑它的只有能 ssh 上这台机器的人——「谁能签证」由 ssh 层解决，不用再写代码。
//
// SQLite 不是常驻服务，就是一个文件。relay 和这个工具各开各的连接读写同一个文件，
// 互不通信（2026-10-14 实测：三个独立进程分别建表、插入、读取，通的）。所以这个
// 工具不需要知道 relay 在跑什么，改完就退出。
//
// ⚠️ 数据库里存的是 key 的明文，因为 relay 每次握手都要比对，不能哈希。
// 所以 hubkey 打印出来的 key 只在这一次出现，别把终端记录贴出去。
//
// 用法（工作目录不限）：
//
//	hubkey issue akise              # 签一张，默认 90 天后过期
//	hubkey issue akise -days 30     # 30 天
//	hubkey issue akise -days 0      # 不过期
//	hubkey list                     # 看所有 key 和状态
//	hubkey show key-xxxx            # 看一张的详情
//	hubkey revoke key-xxxx          # 吊销（软删，记录留着）
//	hubkey delete key-xxxx          # 硬删，连记录一起消失
package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/uncleyumo/go-terminal-hub/remote/relaydb"
)

// defaultDays 是默认有效期。90 天是折中：够长不至于忘了续，
// 够短丢钥匙时暴露窗口有限。-days 0 表示不过期。
const defaultDays = 90

// parseArgs 手写参数解析，不用 flag 包。
//
// 为什么不用 flag：flag 遇到第一个位置参数就停止解析，所以
// 「hubkey issue akise -days 90」会报错，必须写成
// 「hubkey issue -days 90 akise」。签证时敲错的概率不低，
// 而且先打 label 再想天数才是顺手的顺序。
//
// 规则：- 开头的是选项，其余按顺序是位置参数。-- 之后全是位置参数。
func parseArgs(args []string) (opts map[string]string, rest []string) {
	opts = make(map[string]string)
	positional := false

	for i := 0; i < len(args); i++ {
		a := args[i]
		if positional || !strings.HasPrefix(a, "-") || a == "-" {
			rest = append(rest, a)
			continue
		}
		if a == "--" {
			positional = true
			continue
		}
		// 支持 -days=30 和 -days 30 两种写法
		name := strings.TrimLeft(a, "-")
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			opts[name[:eq]] = name[eq+1:]
			continue
		}
		if i+1 >= len(args) {
			fail("选项 %s 缺一个值", a)
		}
		opts[name] = args[i+1]
		i++
	}
	return opts, rest
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	opts, rest := parseArgs(os.Args[2:])

	dbPath := opts["db"]
	if dbPath == "" {
		dbPath = defaultDBPath()
	}

	switch cmd {
	case "issue":
		days := defaultDays
		if v, ok := opts["days"]; ok {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				fail("-days 要是一个不小于 0 的整数，现在是 %q", v)
			}
			days = n
		}
		if len(rest) != 1 {
			fail("用法：hubkey issue <label> [-days N]")
		}
		issue(dbPath, rest[0], days)

	case "list":
		list(dbPath)

	case "show":
		if len(rest) != 1 {
			fail("用法：hubkey show <key>")
		}
		show(dbPath, rest[0])

	case "revoke":
		if len(rest) != 1 {
			fail("用法：hubkey revoke <key>")
		}
		revoke(dbPath, rest[0])

	case "delete":
		if len(rest) != 1 {
			fail("用法：hubkey delete <key>")
		}
		del(dbPath, rest[0])

	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `hubkey —— 使用 key 的管理工具

  hubkey issue <label> [-days N]   签发一张（默认 90 天，0 表示不过期）
  hubkey list                      列出所有 key
  hubkey show <key>                看一张的详情
  hubkey revoke <key>              吊销（记录保留，可恢复）
  hubkey delete <key>              删除（连记录一起消失，不可恢复）

  -db <path>                       指定数据库位置
`)
}

// defaultDBPath 取默认数据库位置。
//
// 值来自 relaydb.DefaultPath——一个两边共用的常量。这里不再自己写一份：
// 曾经这里硬编码 /var/lib/hub-relay/relay.db，而 relay 那边是 systemd 的
// EnvironmentFile 喂的路径，两个来源各写一份，改一处漏一处，
// 症状是「签证成功了但连不上」，两边都不报错。
//
// HUB_DB 环境变量保留，但它和 -db 是同一样东西的两种写法，都不覆盖部署时的
// 真实位置：relay 的 systemd 单元里写的是 -db，那份才算数。
func defaultDBPath() string {
	if p := os.Getenv("HUB_DB"); p != "" {
		return p
	}
	return relaydb.DefaultPath
}

// open 打开数据库并建表。
//
// 三个刻意的参数（busy_timeout、WAL、单连接）见 relaydb.Open 的注释。
func open(path string) *sql.DB {
	db, err := relaydb.Open(path)
	if err != nil {
		fail("打不开数据库：%v\n  路径：%s", err, path)
	}
	return db
}

// issue 签一张新 key 并打印。
//
// 用 crypto/rand 而不是时间戳拼字符串：key 就是门牌号，猜得中就能连上。
func issue(dbPath, label string, days int) {
	label = strings.TrimSpace(label)
	if label == "" {
		fail("label 不能为空。它是给你自己看的备注，比如朋友的昵称。")
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败说明进程环境不可信，没有降级方案
		fail("取随机数失败：%v", err)
	}
	key := "key-" + base64.RawURLEncoding.EncodeToString(b)

	now := time.Now()
	var expires int64
	if days > 0 {
		expires = now.AddDate(0, 0, days).Unix()
	} else {
		expires = 0 // 0 = 不过期
	}

	db := open(dbPath)
	defer db.Close()

	_, err := db.Exec(`INSERT INTO keys (id, label, created_at, expires_at, revoked)
	                   VALUES (?, ?, ?, ?, 0)`, key, label, now.Unix(), expires)
	if err != nil {
		fail("写入失败：%v", err)
	}

	fmt.Println(key)
	fmt.Fprintf(os.Stderr, "\n已登记：%s", label)
	if expires == 0 {
		fmt.Fprint(os.Stderr, "，不过期")
	} else {
		fmt.Fprintf(os.Stderr, "，%s 过期", time.Unix(expires, 0).Format("2006-01-02"))
	}
	fmt.Fprint(os.Stderr, "\n⚠️ 这个 key 只显示这一次，数据库里是明文但不会再打印。\n")
}

func list(dbPath string) {
	db := open(dbPath)
	defer db.Close()

	rows, err := db.Query(`SELECT id, label, created_at, expires_at, revoked
	                       FROM keys ORDER BY created_at DESC`)
	if err != nil {
		fail("查询失败：%v", err)
	}
	defer rows.Close()

	now := time.Now().Unix()
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tLABEL\tCREATED\tEXPIRES\tSTATE")
	n := 0
	for rows.Next() {
		var id, label string
		var created, expires, revoked int64
		if err := rows.Scan(&id, &label, &created, &expires, &revoked); err != nil {
			fail("读取失败：%v", err)
		}
		n++
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			id, label,
			time.Unix(created, 0).Format("2006-01-02"),
			expiryText(expires, now),
			stateText(expires, revoked, now))
	}
	if err := rows.Err(); err != nil {
		fail("遍历失败：%v", err)
	}
	w.Flush()
	if n == 0 {
		fmt.Println("（还没有任何 key）")
	}
}

func show(dbPath, key string) {
	db := open(dbPath)
	defer db.Close()

	var label string
	var created, expires, revoked int64
	err := db.QueryRow(`SELECT label, created_at, expires_at, revoked FROM keys WHERE id = ?`, key).
		Scan(&label, &created, &expires, &revoked)
	if err == sql.ErrNoRows {
		fail("没有这个 key：%s", key)
	}
	if err != nil {
		fail("查询失败：%v", err)
	}

	now := time.Now().Unix()
	fmt.Printf("key        %s\n", key)
	fmt.Printf("label      %s\n", label)
	fmt.Printf("created    %s\n", time.Unix(created, 0).Format("2006-01-02 15:04"))
	fmt.Printf("expires    %s\n", expiryText(expires, now))
	fmt.Printf("state      %s\n", stateText(expires, revoked, now))
}

// revoke 是软删：把 revoked 置 1，记录留着。
//
// 为什么默认软删而不是直接删：出问题时「谁在什么时候用过哪个 key」是唯一的线索。
// 硬删用 delete，那是明知道要连记录一起消失的场景。
func revoke(dbPath, key string) {
	db := open(dbPath)
	defer db.Close()

	if err := revokeErr(db, key); err != nil {
		fail("%v", err)
	}
	fmt.Printf("已吊销 %s\n", key)
}

// revokeErr 是 revoke 的可测试版本：返回错误而不是退出进程。
//
// 拆开的理由：revoke 要打印人话并退出，而测试需要拿到那个错误去断言。
// 错误在这里成形，两边共用一份文案。
func revokeErr(db *sql.DB, key string) error {
	res, err := db.Exec(`UPDATE keys SET revoked = 1 WHERE id = ?`, key)
	if err != nil {
		return fmt.Errorf("吊销失败：%w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("没有这个 key：%s", key)
	}
	return nil
}

// del 是硬删，记录一起消失。
//
// 吊销过的 key 也能删：revoke 之后的正常收尾。所以这里不检查 revoked。
func del(dbPath, key string) {
	db := open(dbPath)
	defer db.Close()

	res, err := db.Exec(`DELETE FROM keys WHERE id = ?`, key)
	if err != nil {
		fail("删除失败：%v", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		fail("没有这个 key：%s", key)
	}
	fmt.Printf("已删除 %s\n", key)
}

// expiryText 显示过期时间。0 表示不过期。
func expiryText(expires, now int64) string {
	if expires == 0 {
		return "never"
	}
	return time.Unix(expires, 0).Format("2006-01-02")
}

// stateText 是给 list 看的一列状态。
//
// 四种状态分开写，因为排查时第一眼要看的就是「这个 key 到底能不能用」：
// 吊销、过期、正常、即将过期。
func stateText(expires, revoked, now int64) string {
	if revoked != 0 {
		return "revoked"
	}
	if expires != 0 && now >= expires {
		return "expired"
	}
	if expires != 0 && expires-now < 7*24*3600 {
		return "expiring"
	}
	return "active"
}

// fail 打印错误并退出。
//
// 所有失败路径都走这里：带上路径和上下文，因为最常见的失败就是
// 「数据库不在你以为的地方」和「没权限读写」。
func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "hubkey: "+format+"\n", args...)
	os.Exit(1)
}
