package handler

import (
	"database/sql"
	"net/http"

	"solarpanel/internal/auth"
	"solarpanel/internal/db"
)

type PublicHandler struct{}

func NewPublicHandler() *PublicHandler { return &PublicHandler{} }

type GroupWithItems struct {
	ItemGroupRow
	Items []ItemRow `json:"items"`
}

func (h *PublicHandler) Public(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, max-age=0")

	settingsQ, _ := db.DB.Query("SELECT config_name, config_value FROM settings")
	settings := make(map[string]string)
	for settingsQ.Next() {
		var name, val string
		settingsQ.Scan(&name, &val)
		settings[name] = val
	}
	settingsQ.Close()

	sess := auth.GetSessionFromRequest(r)
	isAdmin := false
	canSeeHiddenItems := false
	var userInfo map[string]any = nil
	guestRequired := false

	if sess != nil {
		if sess.Role == "admin" || sess.Role == "editor" || sess.Role == "viewer" {
			isAdmin = true
		}
		if sess.Role == "admin" || sess.Role == "editor" {
			canSeeHiddenItems = true
		}
		if sess.Role != "guest" {
			var uName, uRole, uDBName string
			err := db.DB.QueryRow("SELECT username, name, role FROM users WHERE id=?", sess.UID).Scan(&uName, &uDBName, &uRole)
			if err == nil {
				userInfo = map[string]any{"username": uName, "name": uDBName, "role": uRole}
			}
		} else {
			userInfo = map[string]any{"role": "guest"}
		}
	} else {
		if settings["guest_access_enabled"] == "1" {
			guestRequired = true
		}
	}

	if guestRequired {
		OK(w, map[string]any{
			"settings":        settings,
			"groups":          []GroupWithItems{},
			"user":            nil,
			"guest_required":  true,
		})
		return
	}

	groupQuery := "SELECT id, parent_id, title, icon, description, sort, is_visible, user_id, created_at FROM item_groups"
	groupArgs := []any{}
	if !isAdmin {
		groupQuery += " WHERE is_visible = 1"
	}
	groupQuery += " ORDER BY parent_id ASC, sort ASC, id ASC"
	groupQ, _ := db.DB.Query(groupQuery, groupArgs...)
	var groups []ItemGroupRow
	for groupQ.Next() {
		var g ItemGroupRow
		groupQ.Scan(&g.ID, &g.ParentID, &g.Title, &g.Icon, &g.Description, &g.Sort, &g.IsVisible, &g.UserID, &g.CreatedAt)
		groups = append(groups, g)
	}
	groupQ.Close()

	itemQ, _ := db.DB.Query("SELECT id, group_id, title, url, lan_url, description, icon_type, icon_value, icon_bg, open_method, sort, is_hidden, user_id, created_at FROM items ORDER BY sort ASC, id ASC")
	var items []ItemRow
	for itemQ.Next() {
		var it ItemRow
		itemQ.Scan(&it.ID, &it.GroupID, &it.Title, &it.URL, &it.LanURL, &it.Description,
			&it.IconType, &it.IconValue, &it.IconBG, &it.OpenMethod, &it.Sort, &it.IsHidden,
			&it.UserID, &it.CreatedAt)
		items = append(items, it)
	}
	itemQ.Close()

	grouped := make([]GroupWithItems, 0, len(groups))
	for _, g := range groups {
		gw := GroupWithItems{ItemGroupRow: g, Items: []ItemRow{}}
		for _, it := range items {
			if it.GroupID != g.ID {
				continue
			}
			if !canSeeHiddenItems && it.IsHidden == 1 {
				continue
			}
			gw.Items = append(gw.Items, it)
		}
		grouped = append(grouped, gw)
	}

	_ = sql.ErrNoRows
	OK(w, map[string]any{
		"settings":       settings,
		"groups":         grouped,
		"user":           userInfo,
		"guest_required": guestRequired,
	})
}
