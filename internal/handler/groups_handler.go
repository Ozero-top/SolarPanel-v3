package handler

import (
	"database/sql"
	"net/http"
	"strings"

	"solarpanel/internal/auth"
	"solarpanel/internal/db"
)

func sanitizeGroupIcon(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '<' || r == '>' {
			return -1
		}
		return r
	}, s)
	if runes := []rune(s); len(runes) > 8 {
		s = string(runes[:8])
	}
	return s
}

type GroupsHandler struct{}

func NewGroupsHandler() *GroupsHandler { return &GroupsHandler{} }

type ItemGroupRow struct {
	ID          int    `json:"id"`
	ParentID    int    `json:"parent_id"`
	Title       string `json:"title"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
	Sort        int    `json:"sort"`
	IsVisible   int    `json:"is_visible"`
	UserID      int    `json:"user_id"`
	CreatedAt   string `json:"created_at"`
}

type groupListItem struct {
	ItemGroupRow
	ItemCount int `json:"item_count"`
}

func (h *GroupsHandler) Dispatch(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Query().Get("action")
	switch action {
	case "":
		h.List(w, r)
	case "list":
		h.List(w, r)
	case "edit":
		h.Edit(w, r)
	case "delete":
		h.Delete(w, r)
	case "sort":
		h.Sort(w, r)
	case "sort_batch":
		h.SortBatch(w, r)
	case "visible":
		h.SetVisible(w, r)
	default:
		Fail(w, "未知操作")
	}
}

func scanItemGroup(row *sql.Row) (*ItemGroupRow, error) {
	var g ItemGroupRow
	err := row.Scan(&g.ID, &g.ParentID, &g.Title, &g.Icon, &g.Description, &g.Sort, &g.IsVisible, &g.UserID, &g.CreatedAt)
	return &g, err
}

func (h *GroupsHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := db.DB.Query("SELECT id, parent_id, title, icon, description, sort, is_visible, user_id, created_at FROM item_groups ORDER BY parent_id ASC, sort ASC, id ASC")
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取分组失败")
		return
	}

	var groups []ItemGroupRow
	for rows.Next() {
		var g ItemGroupRow
		if err := rows.Scan(&g.ID, &g.ParentID, &g.Title, &g.Icon, &g.Description, &g.Sort, &g.IsVisible, &g.UserID, &g.CreatedAt); err != nil {
			continue
		}
		groups = append(groups, g)
	}
	rows.Close() // 必须先关，释放读锁后才能再开 QueryRow

	// 树形展开：一级分组按 sort 排，子分组紧跟所属一级分组之后（同级内按 sort）
	children := make(map[int][]ItemGroupRow)
	for _, g := range groups {
		if g.ParentID != 0 {
			children[g.ParentID] = append(children[g.ParentID], g)
		}
	}
	ordered := make([]ItemGroupRow, 0, len(groups))
	placed := make(map[int]bool, len(groups))
	for _, g := range groups {
		if g.ParentID != 0 {
			continue
		}
		ordered = append(ordered, g)
		placed[g.ID] = true
		for _, c := range children[g.ID] {
			ordered = append(ordered, c)
			placed[c.ID] = true
		}
	}
	// 父分组已不存在的孤儿分组兜底追加，避免数据“消失”
	for _, g := range groups {
		if !placed[g.ID] {
			ordered = append(ordered, g)
		}
	}

	result := make([]groupListItem, 0, len(groups))
	for _, g := range ordered {
		var cnt int
		db.DB.QueryRow("SELECT COUNT(*) FROM items WHERE group_id=?", g.ID).Scan(&cnt)
		result = append(result, groupListItem{ItemGroupRow: g, ItemCount: cnt})
	}
	if result == nil {
		result = []groupListItem{}
	}
	OK(w, result)
}

func (h *GroupsHandler) Edit(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || (sess.Role != "admin" && sess.Role != "editor") {
		Err(w, http.StatusForbidden, 1, "无操作权限")
		return
	}
	var body struct {
		ID          uint   `json:"id"`
		ParentID    int    `json:"parent_id"`
		Title       string `json:"title"`
		Icon        string `json:"icon"`
		Description string `json:"description"`
		Sort        int    `json:"sort"`
		IsVisible   int    `json:"is_visible"`
	}
	if err := BindJSON(r, &body); err != nil {
		Fail(w, "参数错误")
		return
	}
	if body.Title == "" {
		Fail(w, "标题不能为空")
		return
	}
	icon := sanitizeGroupIcon(body.Icon)

	if body.ID == 0 {
		var maxSort sql.NullInt64
		db.DB.QueryRow("SELECT COALESCE(MAX(sort), -1) FROM item_groups WHERE parent_id=?", body.ParentID).Scan(&maxSort)
		newSort := 0
		if maxSort.Valid {
			newSort = int(maxSort.Int64) + 1
		}
		res, err := db.DB.Exec("INSERT INTO item_groups (parent_id, title, icon, description, sort) VALUES (?, ?, ?, ?, ?)",
			body.ParentID, body.Title, icon, body.Description, newSort)
		if err != nil {
			Fail(w, "创建失败")
			return
		}
		lastID, _ := res.LastInsertId()
		g := ItemGroupRow{ID: int(lastID), ParentID: body.ParentID, Title: body.Title, Icon: icon, Description: body.Description, Sort: newSort, IsVisible: 1, UserID: 1}
		OK(w, g)
		return
	}

	tx, err := db.DB.Begin()
	if err != nil {
		Fail(w, "保存失败")
		return
	}
	defer tx.Rollback()

	var oldParent int
	if err := tx.QueryRow("SELECT parent_id FROM item_groups WHERE id=?", body.ID).Scan(&oldParent); err != nil {
		Fail(w, "分组不存在")
		return
	}

	_, err = tx.Exec("UPDATE item_groups SET parent_id=?, title=?, icon=?, description=? WHERE id=?",
		body.ParentID, body.Title, icon, body.Description, body.ID)
	if err != nil {
		Fail(w, "保存失败")
		return
	}

	// 改挂到另一个一级分组时，sort 必须在新父级内重新排到末尾，否则会与同级分组抢同一序号
	if body.ParentID != oldParent {
		var maxSort sql.NullInt64
		tx.QueryRow("SELECT COALESCE(MAX(sort), -1) FROM item_groups WHERE parent_id=? AND id<>?", body.ParentID, body.ID).Scan(&maxSort)
		if _, err := tx.Exec("UPDATE item_groups SET sort=? WHERE id=?", int(maxSort.Int64)+1, body.ID); err != nil {
			Fail(w, "保存失败")
			return
		}
	}

	g, err := scanItemGroup(tx.QueryRow("SELECT id, parent_id, title, icon, description, sort, is_visible, user_id, created_at FROM item_groups WHERE id=?", body.ID))
	if err != nil {
		Fail(w, "分组不存在")
		return
	}

	if err := tx.Commit(); err != nil {
		Fail(w, "保存失败")
		return
	}
	OK(w, g)
}

func (h *GroupsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || (sess.Role != "admin" && sess.Role != "editor") {
		Err(w, http.StatusForbidden, 1, "无操作权限")
		return
	}
	var body struct {
		ID uint `json:"id"`
	}
	if err := BindJSON(r, &body); err != nil || body.ID == 0 {
		Fail(w, "参数错误")
		return
	}
	// 级联处理：子分组升级为顶级（parent_id=0），避免悬空 parent_id
	db.DB.Exec("UPDATE item_groups SET parent_id=0 WHERE parent_id=?", body.ID)
	// 级联删 items（悬空外键清理）
	db.DB.Exec("DELETE FROM items WHERE group_id=?", body.ID)
	db.DB.Exec("DELETE FROM item_groups WHERE id=?", body.ID)
	OK(w, nil)
}

func (h *GroupsHandler) Sort(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || (sess.Role != "admin" && sess.Role != "editor") {
		Err(w, http.StatusForbidden, 1, "无操作权限")
		return
	}
	var body struct {
		ID        uint   `json:"id"`
		Dir       string `json:"dir"`
		Direction string `json:"direction"`
	}
	if err := BindJSON(r, &body); err != nil || body.ID == 0 {
		Fail(w, "参数错误")
		return
	}
	dir := body.Dir
	if dir == "" {
		dir = body.Direction
	}

	// 排序只在同级（相同 parent_id）内进行，一级分组与二级分组互不干扰
	var curSort, curParent int
	err := db.DB.QueryRow("SELECT sort, parent_id FROM item_groups WHERE id=?", body.ID).Scan(&curSort, &curParent)
	if err != nil {
		Fail(w, "分组不存在")
		return
	}

	var otherSort int
	if dir == "up" {
		err = db.DB.QueryRow("SELECT sort FROM item_groups WHERE parent_id=? AND sort < ? ORDER BY sort DESC, id DESC LIMIT 1", curParent, curSort).Scan(&otherSort)
	} else {
		err = db.DB.QueryRow("SELECT sort FROM item_groups WHERE parent_id=? AND sort > ? ORDER BY sort ASC, id ASC LIMIT 1", curParent, curSort).Scan(&otherSort)
	}
	if err != nil {
		Fail(w, map[string]string{"up": "已经是第一个", "down": "已经是最后一个"}[dir])
		return
	}

	db.DB.Exec("UPDATE item_groups SET sort=? WHERE id=?", otherSort, body.ID)
	// 找 other 的 id 来更新
	var otherID int
	if dir == "up" {
		db.DB.QueryRow("SELECT id FROM item_groups WHERE parent_id=? AND sort=? AND id<>? ORDER BY id ASC LIMIT 1", curParent, otherSort, body.ID).Scan(&otherID)
	} else {
		db.DB.QueryRow("SELECT id FROM item_groups WHERE parent_id=? AND sort=? AND id<>? ORDER BY id DESC LIMIT 1", curParent, otherSort, body.ID).Scan(&otherID)
	}
	if otherID > 0 {
		db.DB.Exec("UPDATE item_groups SET sort=? WHERE id=?", curSort, otherID)
	}
	OK(w, nil)
}

func (h *GroupsHandler) SortBatch(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || (sess.Role != "admin" && sess.Role != "editor") {
		Err(w, http.StatusForbidden, 1, "无操作权限")
		return
	}
	var body struct {
		IDs []uint `json:"ids"`
	}
	if err := BindJSON(r, &body); err != nil || len(body.IDs) == 0 {
		Fail(w, "参数错误")
		return
	}

	tx, _ := db.DB.Begin()
	// 序号按父级各自编号：一级分组从 0 递增，每个父级下的二级分组也各自从 0 递增
	counters := make(map[int]int)
	for _, id := range body.IDs {
		var parentID int
		if err := tx.QueryRow("SELECT parent_id FROM item_groups WHERE id=?", id).Scan(&parentID); err != nil {
			continue
		}
		idx := counters[parentID]
		if _, err := tx.Exec("UPDATE item_groups SET sort=? WHERE id=?", idx, id); err != nil {
			tx.Rollback()
			Fail(w, "排序失败")
			return
		}
		counters[parentID] = idx + 1
	}
	tx.Commit()
	OK(w, nil)
}

func (h *GroupsHandler) SetVisible(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || (sess.Role != "admin" && sess.Role != "editor") {
		Err(w, http.StatusForbidden, 1, "无操作权限")
		return
	}
	var body struct {
		ID      uint `json:"id"`
		Visible int  `json:"visible"`
	}
	if err := BindJSON(r, &body); err != nil || body.ID == 0 {
		Fail(w, "参数错误")
		return
	}
	if body.Visible != 0 {
		body.Visible = 1
	}
	db.DB.Exec("UPDATE item_groups SET is_visible=? WHERE id=?", body.Visible, body.ID)
	OK(w, nil)
}
