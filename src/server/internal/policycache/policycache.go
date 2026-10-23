// Package policycache 缓存管理服务下发的命令策略规则。
//
// 为什么在服务端本地判定：SSH 交互中的每个回车都要出结果，
// 逐条命令回源管理服务会引入不可接受的延迟与单点依赖。
// 因此策略以"管理服务为准、服务端缓存"的方式下发，管理服务不可达时
// 继续用最后一次成功加载的规则集，并在状态中暴露降级信息。
package policycache

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"bastion.local/server/internal/managerclient"
	"bastion.local/shared/policy"
)

// Cache 是命令策略缓存。
type Cache struct {
	client   *managerclient.Client
	interval time.Duration

	mu       sync.RWMutex
	engine   *policy.Engine
	rules    int
	loadedAt time.Time
	lastErr  string
}

// New 创建缓存。
func New(client *managerclient.Client, interval time.Duration) *Cache {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	return &Cache{client: client, interval: interval}
}

// Refresh 从管理服务重新加载策略。
func (c *Cache) Refresh(ctx context.Context) error {
	items, err := c.client.CommandPolicies(ctx)
	if err != nil {
		c.mu.Lock()
		c.lastErr = err.Error()
		c.mu.Unlock()
		return err
	}
	engine, err := policy.NewEngine(items, policy.ActionAllow)
	if err != nil {
		c.mu.Lock()
		c.lastErr = err.Error()
		c.mu.Unlock()
		return fmt.Errorf("策略引擎构建失败: %w", err)
	}
	c.mu.Lock()
	c.engine = engine
	c.rules = len(items)
	c.loadedAt = time.Now()
	c.lastErr = ""
	c.mu.Unlock()
	slog.Info("命令策略已刷新", "rules", len(items))
	return nil
}

// Run 定时刷新策略，直到 ctx 结束。
func (c *Cache) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Refresh(rctx)
			cancel()
			if err != nil {
				slog.Warn("命令策略刷新失败，继续使用上次规则", "err", err)
			}
		}
	}
}

// Ready 判断是否已成功加载过策略。
func (c *Cache) Ready() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.engine != nil
}

// Evaluate 判定一条命令。
//
// 引擎未就绪时返回 allow 并在原因中标记降级：SSH 网关在启动前会先
// 强制加载一次策略（Ready 不通过则不开启监听），因此该分支只在
// "运行中策略被清空"这类异常情况下出现，属于最后兜底。
func (c *Cache) Evaluate(ctx policy.EvalContext, command string) *policy.Decision {
	c.mu.RLock()
	engine := c.engine
	c.mu.RUnlock()
	if engine == nil {
		return &policy.Decision{
			Action:    policy.ActionAllow,
			RiskLevel: policy.RiskNormal,
			Reason:    "命令策略未加载（管理服务不可达），本次放行并已告警",
			Matched:   false,
		}
	}
	return engine.Evaluate(ctx, command)
}

// Status 返回缓存状态，用于健康检查。
func (c *Cache) Status() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	st := map[string]any{
		"rules":  c.rules,
		"ready":  c.engine != nil,
		"last_error": c.lastErr,
	}
	if !c.loadedAt.IsZero() {
		st["loaded_at"] = c.loadedAt.Format(time.RFC3339)
		st["age_seconds"] = int(time.Since(c.loadedAt).Seconds())
	}
	return st
}
