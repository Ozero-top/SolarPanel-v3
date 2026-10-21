// cmd/seed/main.go — import WebStackPage parsed JSON into SolarPanel SQLite
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

type Card struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Logo        string `json:"logo"`
}

type Group struct {
	Name   string `json:"name"`
	Parent string `json:"parent"`
	Items  []Card `json:"items"`
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: seed <db_path> <json_path>")
		os.Exit(1)
	}
	dbPath := os.Args[1]
	jsonPath := os.Args[2]

	// Read JSON
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		log.Fatal("read json:", err)
	}
	var groups []Group
	if err := json.Unmarshal(data, &groups); err != nil {
		log.Fatal("parse json:", err)
	}

	// Open DB
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		log.Fatal("open db:", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatal("ping:", err)
	}
	log.Println("DB connected:", dbPath)

	tx, err := db.Begin()
	if err != nil {
		log.Fatal("begin:", err)
	}

	// Clear existing
	if _, err := tx.Exec("DELETE FROM items WHERE user_id = 1"); err != nil {
		log.Fatal("clear items:", err)
	}
	if _, err := tx.Exec("DELETE FROM item_groups WHERE user_id = 1"); err != nil {
		log.Fatal("clear groups:", err)
	}
	log.Println("Cleared old data")

	now := time.Now().Format("2006-01-02 15:04:05")

	// Step 1: insert top-level groups (parent == "")
	type topInfo struct {
		idx int
		gid int64
	}
	topOrder := []topInfo{} // track order + id
	sortIdx := 0

	for i, g := range groups {
		if g.Parent == "" {
			sortIdx++
			desc := "WebStack 导入分组"
			res, err := tx.Exec(
				"INSERT INTO item_groups (title, icon, description, sort, is_visible, user_id, created_at) VALUES (?, '', ?, ?, 1, 1, ?)",
				g.Name, desc, sortIdx, now,
			)
			if err != nil {
				log.Fatal("insert top group", g.Name, ":", err)
			}
			gid, _ := res.LastInsertId()
			topOrder = append(topOrder, topInfo{idx: i, gid: gid})
			fmt.Printf("  TOP [%d] id=%d '%s'\n", sortIdx, gid, g.Name)
		}
	}

	// Step 2: insert child groups
	type childInfo struct {
		idx     int
		gid     int64
		parent  string // top-level group display name (parent field)
	}
	childOrder := []childInfo{}

	for i, g := range groups {
		if g.Parent != "" {
			sortIdx++
			desc := "子分组（父: " + g.Parent + "）"
			res, err := tx.Exec(
				"INSERT INTO item_groups (title, icon, description, sort, is_visible, user_id, created_at) VALUES (?, '', ?, ?, 1, 1, ?)",
				g.Name, desc, sortIdx, now,
			)
			if err != nil {
				log.Fatal("insert child group", g.Name, ":", err)
			}
			gid, _ := res.LastInsertId()
			childOrder = append(childOrder, childInfo{idx: i, gid: gid, parent: g.Parent})
			fmt.Printf("  CHL [%d] id=%d '%s' <- '%s'\n", sortIdx, gid, g.Name, g.Parent)
		}
	}

	// Build group name -> id map
	groupIDMap := map[string]int64{}
	for _, t := range topOrder {
		groupIDMap[groups[t.idx].Name] = t.gid
	}
	for _, c := range childOrder {
		groupIDMap[groups[c.idx].Name] = c.gid
	}

	// Step 3: insert items
	itemSort := 0
	totalItems := 0
	for _, g := range groups {
		gid, ok := groupIDMap[g.Name]
		if !ok {
			log.Println("WARN: group not found:", g.Name)
			continue
		}
		for _, it := range g.Items {
			itemSort++
			desc := it.Description
			if len(desc) > 900 {
				desc = desc[:900]
			}
			_, err := tx.Exec(
				"INSERT INTO items (group_id, title, url, lan_url, description, icon_type, icon_value, icon_bg, open_method, sort, is_hidden, user_id, created_at) VALUES (?, ?, ?, '', ?, 'favicon', '', '', 2, ?, 0, 1, ?)",
				gid, it.Title, it.URL, desc, itemSort, now,
			)
			if err != nil {
				log.Fatal("insert item", it.Title, ":", err)
			}
			totalItems++
		}
	}

	if err := tx.Commit(); err != nil {
		log.Fatal("commit:", err)
	}

	fmt.Printf("\n=== DONE ===\n")
	fmt.Printf("Groups: %d (top=%d, child=%d)\n", len(topOrder)+len(childOrder), len(topOrder), len(childOrder))
	fmt.Printf("Items: %d\n", totalItems)
	fmt.Println("Seed complete!")
}
