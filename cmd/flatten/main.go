// cmd/flatten/main.go — 一次性执行：删除空顶级壳，子分组升顶级
// 运行: go run cmd/flatten/main.go
package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func main() {
	// 找 SolarPanel/data/solarpanel.db
	candidates := []string{
		"data/solarpanel.db",
		"../SolarPanel/data/solarpanel.db",
		"/app/data/solarpanel.db",
	}
	var dbPath string
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			dbPath, _ = filepath.Abs(p)
			break
		}
	}
	if dbPath == "" {
		fmt.Fprintln(os.Stderr, "找不到 solarpanel.db，请在 SolarPanel/ 目录或 Docker 内运行")
		os.Exit(1)
	}
	fmt.Println("使用 DB:", dbPath)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// 1. 列出哪些顶级要删（parent_id=0, 自己没 items, 但有子分组）
	rows, err := db.Query(`
		SELECT id, title FROM item_groups g
		WHERE g.parent_id = 0
		  AND NOT EXISTS (SELECT 1 FROM items i WHERE i.group_id = g.id)
		  AND EXISTS (SELECT 1 FROM item_groups c WHERE c.parent_id = g.id)
	`)
	if err != nil {
		log.Fatal(err)
	}
	var toDelete []int
	for rows.Next() {
		var id int
		var title string
		rows.Scan(&id, &title)
		fmt.Printf("  要删除的顶级壳: id=%d '%s'\n", id, title)
		toDelete = append(toDelete, id)
	}
	rows.Close()

	if len(toDelete) == 0 {
		fmt.Println("没有需要删除的空顶级壳，已打平。退出。")
		return
	}

	// 2. 子分组升顶级：parent_id → 0
	for _, pid := range toDelete {
		res, _ := db.Exec("UPDATE item_groups SET parent_id = 0 WHERE parent_id = ?", pid)
		n, _ := res.RowsAffected()
		fmt.Printf("  parent_id=%d: %d 个子分组升为顶级\n", pid, n)
	}

	// 3. 删除空顶级壳
	for _, pid := range toDelete {
		res, _ := db.Exec("DELETE FROM item_groups WHERE id = ?", pid)
		n, _ := res.RowsAffected()
		fmt.Printf("  删除顶级壳 id=%d: %d 行\n", pid, n)
	}

	// 4. 验证
	fmt.Println("\n=== 验证 ===")
	rows2, _ := db.Query(`
		SELECT g.parent_id, COUNT(*) FROM item_groups g
		GROUP BY g.parent_id ORDER BY g.parent_id
	`)
	for rows2.Next() {
		var pid, n int
		rows2.Scan(&pid, &n)
		fmt.Printf("  parent_id=%d: %d 分组\n", pid, n)
	}
	rows2.Close()

	rows3, _ := db.Query(`
		SELECT id, title, parent_id,
		  (SELECT COUNT(*) FROM items i WHERE i.group_id = g.id) AS item_count
		FROM item_groups g ORDER BY parent_id, sort, id
	`)
	for rows3.Next() {
		var id int
		var title string
		var pid, n int
		rows3.Scan(&id, &title, &pid, &n)
		fmt.Printf("  id=%d [parent=%d] %s (items=%d)\n", id, pid, title, n)
	}
	rows3.Close()

	fmt.Println("\n✅ 打平完成。重启 SolarPanel 容器生效。")
}
