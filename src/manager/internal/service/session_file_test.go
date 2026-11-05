package service

import (
	"context"
	"testing"

	"bastion.local/manager/internal/model"
)

func TestAddFileRejectsBadDirection(t *testing.T) {
	s := NewSessionService(nil, nil, nil, nil)
	_, err := s.AddFile(context.Background(), 1, &model.SessionFile{Direction: "copy", FileName: "a.txt"})
	if err == nil {
		t.Fatal("expected invalid direction")
	}
	_, err = s.AddFile(context.Background(), 1, &model.SessionFile{Direction: model.FileDirectionUpload, FileName: " "})
	if err == nil {
		t.Fatal("expected empty name")
	}
}
