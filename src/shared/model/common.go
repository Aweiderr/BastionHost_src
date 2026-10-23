// Package model 定义跨服务共享的基础模型。
package model

import "time"

// TenantIDSystem 表示系统级（非租户）数据。
const TenantIDSystem int64 = 0

// PageQuery 是统一的分页查询参数。
type PageQuery struct {
	Page     int    // 从 1 开始
	PageSize int    // 默认 20，最大 200
	Keyword  string // 模糊检索关键字
	SortBy   string
	Order    string // asc / desc
}

// Normalize 归一化分页参数。
func (p *PageQuery) Normalize() {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PageSize <= 0 {
		p.PageSize = 20
	}
	if p.PageSize > 200 {
		p.PageSize = 200
	}
	if p.Order != "asc" && p.Order != "desc" {
		p.Order = "desc"
	}
}

// Offset 返回 SQL OFFSET。
func (p *PageQuery) Offset() int { return (p.Page - 1) * p.PageSize }

// PageResult 是统一的分页返回结构。
type PageResult[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

// NewPageResult 构造分页结果。
func NewPageResult[T any](items []T, total int64, p PageQuery) PageResult[T] {
	if items == nil {
		items = []T{}
	}
	return PageResult[T]{Items: items, Total: total, Page: p.Page, PageSize: p.PageSize}
}

// BaseEntity 是业务表的公共字段（与数据库设计文档一致）。
type BaseEntity struct {
	ID         int64     `json:"id"`
	TenantID   int64     `json:"tenant_id"`
	CreateTime time.Time `json:"create_time"`
	UpdateTime time.Time `json:"update_time"`
	CreateBy   int64     `json:"create_by,omitempty"`
	UpdateBy   int64     `json:"update_by,omitempty"`
}

// 用户状态
const (
	UserStatusActive   = "active"
	UserStatusLocked   = "locked"
	UserStatusDisabled = "disabled"
)

// 内置角色码（与规划文档"超级管理员 + 三员"一致）
const (
	RoleSuperAdmin  = "super_admin"  // 超级管理员：仅初始化与紧急恢复
	RoleSysAdmin    = "sys_admin"    // 系统管理员
	RoleSecAdmin    = "sec_admin"    // 安全管理员
	RoleAuditAdmin  = "audit_admin"  // 审计管理员
	RoleOperator    = "operator"     // 运维人员
	RoleApprover    = "approver"     // 审批人
)

// AllRoles 返回全部内置角色码。
func AllRoles() []string {
	return []string{RoleSuperAdmin, RoleSysAdmin, RoleSecAdmin, RoleAuditAdmin, RoleOperator, RoleApprover}
}
