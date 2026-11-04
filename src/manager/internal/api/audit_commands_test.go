package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuditCommandFilterReadsSessionNoAndID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/audit/commands?session_no=%20S202611040957198918%20&session_id=42&keyword=whoami&page=2&page_size=10",
		nil)
	f := auditCommandFilter(req, 3)
	if f.TenantID != 3 || f.SessionID != 42 || f.SessionNo != "S202611040957198918" {
		t.Fatalf("filter = %+v", f)
	}
	if f.Keyword != "whoami" || f.Page != 2 || f.PageSize != 10 {
		t.Fatalf("filter = %+v", f)
	}
}

func TestAuditCommandFilterKeepsSessionIDWhenSessionNoEmpty(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/commands?session_no=%20%20&session_id=9", nil)
	f := auditCommandFilter(req, 1)
	if f.SessionNo != "" || f.SessionID != 9 {
		t.Fatalf("filter = %+v", f)
	}
}

func TestAuditCommandFilterReadsOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/commands?command_origin=%20client_auto%20", nil)
	f := auditCommandFilter(req, 1)
	if f.CommandOrigin != "client_auto" {
		t.Fatalf("filter = %+v", f)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/audit/commands?command_origin=user", nil)
	f = auditCommandFilter(req, 1)
	if f.CommandOrigin != "user" {
		t.Fatalf("filter = %+v", f)
	}
}

func TestAuditCommandFilterDropsUnknownOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/commands?command_origin=auto", nil)
	f := auditCommandFilter(req, 1)
	if f.CommandOrigin != "" {
		t.Fatalf("unknown origin must not filter: %+v", f)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/audit/commands", nil)
	f = auditCommandFilter(req, 1)
	if f.CommandOrigin != "" {
		t.Fatalf("missing origin must show all: %+v", f)
	}
}

func TestAuditCommandFilterIgnoresNonPositiveSessionID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/commands?session_id=0&user_id=-1", nil)
	f := auditCommandFilter(req, 1)
	if f.SessionID != 0 || f.UserID != 0 {
		t.Fatalf("filter = %+v", f)
	}
}
