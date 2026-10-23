package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"bastion.local/shared/httpx"
	"bastion.local/shared/internalauth"
)

// registerInternalKill 注册管理面 → 代理节点的会话掐断接口。
func registerInternalKill(mux *http.ServeMux, deps Deps) {
	if deps.SSH == nil {
		return
	}
	mux.HandleFunc("POST /internal/v1/sessions/{id}/kill", func(w http.ResponseWriter, r *http.Request) {
		if err := verifyInternalRequest(deps, r); err != nil {
			httpx.Fail(w, err)
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			httpx.FailCode(w, "API_4000")
			return
		}
		var req struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
		ok, err := deps.SSH.KillSession(id, req.Reason)
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		httpx.OK(w, map[string]any{
			"session_id": id,
			"killed":     ok,
			"node_id":    deps.Config.Node.ID,
			"time":       time.Now().Format(time.RFC3339),
		})
	})
}

func verifyInternalRequest(deps Deps, r *http.Request) error {
	secret := deps.Config.Manager.Secret
	ts, err := internalauth.ParseTimestamp(r.Header.Get(internalauth.HeaderTimestamp))
	if err != nil {
		return err
	}
	return internalauth.Verify(secret,
		r.Header.Get(internalauth.HeaderNode),
		r.Header.Get(internalauth.HeaderSignature),
		ts, r.Method, r.URL.RequestURI(), 5*time.Minute)
}
