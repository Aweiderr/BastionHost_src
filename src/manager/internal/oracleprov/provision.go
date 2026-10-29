package oracleprov

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// 管理面直接连目标库，不经过协议网关。
//
// server/internal/proxy 的 Oracle 网关是授权后的 TNS 隧道：它不改写认证包，
// 也没有执行管理 SQL 的通道。CREATE USER 若从运维会话送进去，还会命中
// 「账号与授权变更」审计规则。建库用户因此用资产上已托管的高权限账号直连。

// ErrUserExists 表示目标库已有同名用户或角色。调用方不得改它的口令。
var ErrUserExists = errors.New("oracle user already exists")

// ErrUserMissing 表示要改口令的用户在库里不存在。
var ErrUserMissing = errors.New("oracle user does not exist")

// Target 是资产台账上的 Oracle 连接目标。服务名和 SID 都有时只用服务名，与网关一致。
type Target struct {
	Host        string
	Port        int
	ServiceName string
	SID         string
}

// Admin 是用来执行 DDL 的高权限账号。SysDBA 仅用于 SYS。
type Admin struct {
	Username string
	Password string
	SysDBA   bool
}

// Session 是一次到目标库的管理连接。
type Session interface {
	UserExists(ctx context.Context, username string) (bool, error)
	Exec(ctx context.Context, statement string) error
	Close() error
}

// Opener 打开管理连接。测试注入替身，生产使用 Open。
type Opener func(ctx context.Context, target Target, admin Admin) (Session, error)

// Provision 在库中创建用户并授予 CONNECT、RESOURCE。
// 用户已存在时返回 ErrUserExists，不执行 ALTER USER。
// 授权失败时删除刚创建的用户（不用 CASCADE），让管理员可以原样重试。
func Provision(ctx context.Context, open Opener, target Target, admin Admin, username, password string) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	if err := ValidatePassword(password); err != nil {
		return err
	}
	user := CanonicalUsername(username)
	sess, err := dial(ctx, open, target, admin)
	if err != nil {
		return classify("连接 Oracle", err, admin.Password, password)
	}
	defer sess.Close()

	exists, err := sess.UserExists(ctx, user)
	if err != nil {
		return classify("查询 Oracle 用户", err, admin.Password, password)
	}
	if exists {
		return ErrUserExists
	}
	if err := sess.Exec(ctx, CreateUserSQL(user, password)); err != nil {
		return classify("创建 Oracle 用户", err, admin.Password, password)
	}
	if err := sess.Exec(ctx, GrantMinimalSQL(user)); err != nil {
		grantErr := classify("授予 CONNECT、RESOURCE", err, admin.Password, password)
		if dropErr := sess.Exec(ctx, DropUserSQL(user)); dropErr != nil {
			return fmt.Errorf("%v；已尝试删除刚创建的用户 %s 但失败：%v", grantErr, user,
				classify("删除 Oracle 用户", dropErr, admin.Password, password))
		}
		return fmt.Errorf("%v；已删除刚创建的用户 %s", grantErr, user)
	}
	return nil
}

// AlterPassword 修改已有用户的口令。用户不存在时返回 ErrUserMissing，不创建用户。
func AlterPassword(ctx context.Context, open Opener, target Target, admin Admin, username, password string) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	if err := ValidatePassword(password); err != nil {
		return err
	}
	user := CanonicalUsername(username)
	sess, err := dial(ctx, open, target, admin)
	if err != nil {
		return classify("连接 Oracle", err, admin.Password, password)
	}
	defer sess.Close()

	exists, err := sess.UserExists(ctx, user)
	if err != nil {
		return classify("查询 Oracle 用户", err, admin.Password, password)
	}
	if !exists {
		return ErrUserMissing
	}
	if err := sess.Exec(ctx, AlterPasswordSQL(user, password)); err != nil {
		return classify("修改 Oracle 用户口令", err, admin.Password, password)
	}
	return nil
}

func dial(ctx context.Context, open Opener, target Target, admin Admin) (Session, error) {
	if open == nil {
		open = Open
	}
	if strings.TrimSpace(admin.Username) == "" || admin.Password == "" {
		return nil, errString("Oracle 管理员账号或口令为空")
	}
	return open(ctx, target, admin)
}

func classify(action string, err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrUserExists) || errors.Is(err, ErrUserMissing) {
		return err
	}
	raw := err.Error()
	switch {
	case strings.Contains(raw, "ORA-01920"):
		return ErrUserExists
	case strings.Contains(raw, "ORA-01918"):
		return ErrUserMissing
	case strings.Contains(raw, "ORA-65096"):
		return errString("当前连接到 Oracle 根容器，普通用户名必须以 C## 开头，或把资产的服务名改为 PDB")
	case strings.Contains(raw, "ORA-01031"):
		return fmt.Errorf("%s失败：管理员权限不足（需要 CREATE USER，以及把 CONNECT、RESOURCE 授出的权限）", action)
	case strings.Contains(raw, "ORA-01017"):
		return fmt.Errorf("%s失败：Oracle 拒绝了管理员登录（口令错误或账号被锁定）", action)
	default:
		return fmt.Errorf("%s失败：%s", action, Redact(raw, secrets...))
	}
}
