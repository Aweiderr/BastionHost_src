package service

import (
	"sync"
	"time"
)

// LiveSessionRegistry 保存每个代理节点最近一次心跳里的在途会话清单。
//
// 只放内存：清单是"这一拍进程里有哪些连接"，不是审计事实。
// 写进数据库会在管理服务重启后变成过期结论，空清单可能把还活着的会话收掉。
// 管理服务重启后巡检会先等一轮心跳，再根据新上报对账。
type LiveSessionRegistry struct {
	mu    sync.Mutex
	nodes map[string]liveNodeReport
}

// liveNodeReport 是一个节点在某一拍心跳上的在途会话视图。
type liveNodeReport struct {
	reported bool
	ids      map[int64]struct{}
	at       time.Time
}

// NewLiveSessionRegistry 创建登记表。
func NewLiveSessionRegistry() *LiveSessionRegistry {
	return &LiveSessionRegistry{nodes: map[string]liveNodeReport{}}
}

// Observe 记录一次心跳。reported 为 false 时忽略 ids（旧版代理没报清单）。
func (r *LiveSessionRegistry) Observe(nodeID string, ids []int64, reported bool) {
	if r == nil || nodeID == "" {
		return
	}
	var set map[int64]struct{}
	if reported {
		set = make(map[int64]struct{}, len(ids))
		for _, id := range ids {
			if id > 0 {
				set[id] = struct{}{}
			}
		}
	}
	r.mu.Lock()
	r.nodes[nodeID] = liveNodeReport{reported: reported, ids: set, at: time.Now()}
	r.mu.Unlock()
}

func (r *LiveSessionRegistry) snapshot() map[string]liveNodeReport {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]liveNodeReport, len(r.nodes))
	for id, rep := range r.nodes {
		out[id] = rep
	}
	return out
}
