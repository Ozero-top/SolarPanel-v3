package handler

import (
	"net/http"
	"strings"

	"solarpanel/internal/auth"
	"solarpanel/internal/db"
	"golang.org/x/crypto/bcrypt"
)

type UserHandler struct{}

func NewUserHandler() *UserHandler { return &UserHandler{} }

var validRoles = map[string]bool{"admin": true, "editor": true, "viewer": true, "guest": true}

func isValidRole(role string) bool { return validRoles[role] }

type UserRow struct {
	ID           int    `json:"id"`
	Username     string `json:"username"`
	Name         string `json:"name"`
	Password     string `json:"-"`
	Status       int    `json:"status"`
	Role         string `json:"role"`
	TOTPSecret   string `json:"-"`
	CreatedAt    string `json:"created_at"`
}

func (h *UserHandler) Dispatch(w http.ResponseWriter, r *http.Request) {
	method := r.Method
	action := r.URL.Query().Get("action")
	if action == "" {
		action = r.FormValue("action")
	}

	if method == "GET" {
		if action == "list" || action == "" {
			h.List(w, r)
			return
		}
		Fail(w, "未知操作")
		return
	}

	if method == "POST" {
		switch action {
		case "create":
			h.Create(w, r)
		case "update":
			h.Update(w, r)
		case "delete":
			h.Delete(w, r)
		case "change_password":
			h.ChangePassword(w, r)
		case "set_role":
			h.SetRole(w, r)
		case "set_password":
			h.SetPassword(w, r)
		default:
			Fail(w, "未知操作")
		}
		return
	}
	Fail(w, "不支持的请求方法")
}

func (h *UserHandler) List(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil {
		Err(w, http.StatusUnauthorized, 1, "未登录")
		return
	}
	if sess.Role != "admin" {
		Fail(w, "权限不足")
		return
	}

	rows, err := db.DB.Query("SELECT id, username, name, password, status, role, totp_secret, created_at FROM users")
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取用户失败")
		return
	}
	defer rows.Close()

	type userOut struct {
		ID       int    `json:"id"`
		Username string `json:"username"`
		Name     string `json:"name"`
		Status   int    `json:"status"`
		Role     string `json:"role"`
	}
	var out []userOut
	for rows.Next() {
		var u UserRow
		rows.Scan(&u.ID, &u.Username, &u.Name, &u.Password, &u.Status, &u.Role, &u.TOTPSecret, &u.CreatedAt)
		out = append(out, userOut{ID: u.ID, Username: u.Username, Name: u.Name, Status: u.Status, Role: u.Role})
	}
	if out == nil {
		out = []userOut{}
	}

	OK(w, map[string]any{"users": out, "current_id": sess.UID})
}

func (h *UserHandler) Create(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || sess.Role != "admin" {
		Err(w, http.StatusForbidden, 1, "仅管理员可创建用户")
		return
	}

	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Name     string `json:"name"`
		Role     string `json:"role"`
	}
	if err := BindJSON(r, &body); err != nil {
		Fail(w, "参数错误")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	body.Password = strings.TrimSpace(body.Password)
	body.Name = strings.TrimSpace(body.Name)
	body.Role = strings.TrimSpace(body.Role)
	if body.Username == "" || body.Password == "" {
		Fail(w, "用户名和密码不能为空")
		return
	}
	if len(body.Password) < 6 || len(body.Password) > 64 {
		Fail(w, "密码至少 6 位，至多 64 位")
		return
	}
	if body.Role == "" {
		body.Role = "admin"
	}
	if !isValidRole(body.Role) {
		Fail(w, "权限组不合法")
		return
	}
	if body.Name == "" {
		body.Name = body.Username
	}

	var count int
	db.DB.QueryRow("SELECT COUNT(*) FROM users WHERE username=?", body.Username).Scan(&count)
	if count > 0 {
		Fail(w, "用户名已存在")
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "加密密码失败")
		return
	}

	res, err := db.DB.Exec("INSERT INTO users (username, password, name, status, role) VALUES (?,?,?,?,?)",
		body.Username, string(hashed), body.Name, 1, body.Role)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "创建用户失败")
		return
	}
	lastID, _ := res.LastInsertId()
	OK(w, map[string]int64{"id": lastID})
}

func (h *UserHandler) Update(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil {
		Err(w, http.StatusUnauthorized, 1, "未登录")
		return
	}
	if sess.Role == "viewer" {
		Err(w, http.StatusForbidden, 1, "只读账号无权修改显示名称")
		return
	}

	var body struct {
		ID     uint   `json:"id"`
		Name   string `json:"name"`
		Status int    `json:"status"`
	}
	if err := BindJSON(r, &body); err != nil {
		Fail(w, "参数错误")
		return
	}

	targetID := int(sess.UID)
	if body.ID != 0 && int(body.ID) != int(sess.UID) && sess.Role == "admin" {
		targetID = int(body.ID)
	}

	var existingUser UserRow
	err := db.DB.QueryRow("SELECT id, username, name, password, status, role, totp_secret, created_at FROM users WHERE id=?", targetID).Scan(
		&existingUser.ID, &existingUser.Username, &existingUser.Name, &existingUser.Password, &existingUser.Status, &existingUser.Role, &existingUser.TOTPSecret, &existingUser.CreatedAt)
	if err != nil {
		Fail(w, "用户不存在")
		return
	}

	if sess.Role == "admin" && body.Status != 0 {
		existingUser.Status = body.Status
	}
	if body.Name != "" {
		existingUser.Name = body.Name
	}

	db.DB.Exec("UPDATE users SET name=?, status=? WHERE id=?", existingUser.Name, existingUser.Status, targetID)
	OK(w, map[string]string{"name": existingUser.Name})
}

func (h *UserHandler) Delete(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || sess.Role != "admin" {
		Err(w, http.StatusForbidden, 1, "仅管理员可删除用户")
		return
	}

	var body struct {
		ID uint `json:"id"`
	}
	if err := BindJSON(r, &body); err != nil || body.ID == 0 {
		Fail(w, "参数错误")
		return
	}

	var target UserRow
	err := db.DB.QueryRow("SELECT id, username, name, password, status, role, totp_secret, created_at FROM users WHERE id=?", body.ID).Scan(
		&target.ID, &target.Username, &target.Name, &target.Password, &target.Status, &target.Role, &target.TOTPSecret, &target.CreatedAt)
	if err != nil {
		Fail(w, "用户不存在")
		return
	}
	if target.ID == int(sess.UID) {
		Fail(w, "不能删除自己")
		return
	}

	var totalCount int
	db.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&totalCount)
	if totalCount <= 1 {
		Fail(w, "至少需要保留一个用户")
		return
	}
	if target.Role == "admin" {
		var adminCount int
		db.DB.QueryRow("SELECT COUNT(*) FROM users WHERE role=?", "admin").Scan(&adminCount)
		if adminCount <= 1 {
			Fail(w, "至少保留一个管理员账号")
			return
		}
	}

	db.DB.Exec("DELETE FROM users WHERE id=?", body.ID)
	OK(w, nil)
}

func (h *UserHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil {
		Err(w, http.StatusUnauthorized, 1, "未登录")
		return
	}

	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := BindJSON(r, &body); err != nil {
		Fail(w, "参数错误")
		return
	}
	if body.OldPassword == "" || body.NewPassword == "" {
		Fail(w, "旧密码和新密码不能为空")
		return
	}
	if len(body.NewPassword) < 6 || len(body.NewPassword) > 64 {
		Fail(w, "新密码至少 6 位，至多 64 位")
		return
	}

	var u UserRow
	err := db.DB.QueryRow("SELECT id, username, name, password, status, role, totp_secret, created_at FROM users WHERE id=?", sess.UID).Scan(
		&u.ID, &u.Username, &u.Name, &u.Password, &u.Status, &u.Role, &u.TOTPSecret, &u.CreatedAt)
	if err != nil {
		Fail(w, "用户不存在")
		return
	}
	if u.Role == "viewer" {
		Err(w, http.StatusForbidden, 1, "只读账号无权修改密码")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(body.OldPassword)); err != nil {
		Fail(w, "旧密码错误")
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "加密密码失败")
		return
	}
	db.DB.Exec("UPDATE users SET password=? WHERE id=?", string(hashed), sess.UID)
	OK(w, nil)
}

func (h *UserHandler) SetRole(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || sess.Role != "admin" {
		Err(w, http.StatusForbidden, 1, "仅管理员可修改角色")
		return
	}

	var body struct {
		ID   uint   `json:"id"`
		Role string `json:"role"`
	}
	if err := BindJSON(r, &body); err != nil {
		Fail(w, "参数错误")
		return
	}
	if !isValidRole(body.Role) {
		Fail(w, "权限组不合法")
		return
	}
	if body.ID == sess.UID && body.Role != "admin" {
		Fail(w, "不能修改当前登录账号的权限组")
		return
	}

	var currentRole string
	err := db.DB.QueryRow("SELECT role FROM users WHERE id=?", body.ID).Scan(&currentRole)
	if err != nil {
		Fail(w, "用户不存在")
		return
	}
	if currentRole == "admin" && body.Role != "admin" {
		var adminCount int
		db.DB.QueryRow("SELECT COUNT(*) FROM users WHERE role=?", "admin").Scan(&adminCount)
		if adminCount <= 1 {
			Fail(w, "至少保留一个管理员")
			return
		}
	}
	db.DB.Exec("UPDATE users SET role=? WHERE id=?", body.Role, body.ID)
	OK(w, nil)
}

func (h *UserHandler) SetPassword(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil {
		Err(w, http.StatusUnauthorized, 1, "未登录")
		return
	}

	var body struct {
		ID       uint   `json:"id"`
		Password string `json:"password"`
	}
	if err := BindJSON(r, &body); err != nil {
		Fail(w, "参数错误")
		return
	}
	if len(body.Password) < 6 || len(body.Password) > 64 {
		Fail(w, "新密码至少 6 位，至多 64 位")
		return
	}

	var existingID int
	err := db.DB.QueryRow("SELECT id FROM users WHERE id=?", body.ID).Scan(&existingID)
	if err != nil {
		Fail(w, "用户不存在")
		return
	}
	if sess.Role != "admin" {
		Fail(w, "仅管理员可重置密码")
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "加密密码失败")
		return
	}
	db.DB.Exec("UPDATE users SET password=? WHERE id=?", string(hashed), body.ID)
	OK(w, nil)
}
