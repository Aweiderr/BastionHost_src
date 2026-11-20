package service

import (
	"context"
	"sync"
	"time"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
)

type memChallenges struct {
	mu   sync.Mutex
	rows map[string]*repo.LoginChallenge
}

func newMemChallenges() *memChallenges {
	return &memChallenges{rows: map[string]*repo.LoginChallenge{}}
}

func (m *memChallenges) InvalidateByUser(_ context.Context, userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range m.rows {
		if row.UserID == userID && row.Status == repo.ChallengePending {
			row.Status = repo.ChallengeExpired
		}
	}
	return nil
}

func (m *memChallenges) Create(_ context.Context, c *repo.LoginChallenge) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *c
	cp.Status = repo.ChallengePending
	if cp.CreateTime.IsZero() {
		cp.CreateTime = time.Now()
	}
	m.rows[c.ChallengeID] = &cp
	c.Status = repo.ChallengePending
	c.CreateTime = cp.CreateTime
	return nil
}

func (m *memChallenges) Get(_ context.Context, challengeID string) (*repo.LoginChallenge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[challengeID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *row
	return &cp, nil
}

func (m *memChallenges) MarkStatus(_ context.Context, challengeID, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[challengeID]
	if !ok {
		return repo.ErrNotFound
	}
	row.Status = status
	if status == repo.ChallengeCompleted {
		now := time.Now()
		row.CompleteTime = &now
	}
	return nil
}

func (m *memChallenges) BumpAttempts(_ context.Context, challengeID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[challengeID]
	if !ok {
		return 0, repo.ErrNotFound
	}
	row.Attempts++
	return row.Attempts, nil
}

type memRecoveryCode struct {
	id   int64
	hash string
	used bool
}

func (m *memAuthUsers) userLocked(userID int64) *model.User {
	for _, u := range m.byName {
		if u != nil && u.UserID == userID {
			return u
		}
	}
	return nil
}

func (m *memAuthUsers) SetTOTPPending(_ context.Context, userID int64, secretCipher string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.userLocked(userID)
	if u == nil {
		return repo.ErrNotFound
	}
	u.MFASecretCipher = secretCipher
	u.MFAEnabled = false
	u.MFATotpPending = true
	u.MFATotpLastStep = nil
	return nil
}

func (m *memAuthUsers) BindTOTP(_ context.Context, userID int64, secretCipher string, step int64, hashes []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.userLocked(userID)
	if u == nil {
		return repo.ErrNotFound
	}
	if u.MFAType != "totp" {
		return repo.ErrNotFound
	}
	u.MFAEnabled = true
	u.MFAType = "totp"
	u.MFAFactorActivated = true
	u.MFASecretCipher = secretCipher
	u.MFATotpPending = false
	stepCopy := step
	u.MFATotpLastStep = &stepCopy
	if m.recovery == nil {
		m.recovery = map[int64][]memRecoveryCode{}
	}
	codes := make([]memRecoveryCode, 0, len(hashes))
	for _, h := range hashes {
		m.nextRecovery++
		codes = append(codes, memRecoveryCode{id: m.nextRecovery, hash: h})
	}
	m.recovery[userID] = codes
	return nil
}

func (m *memAuthUsers) ListUnusedRecovery(_ context.Context, userID int64) ([]repo.TOTPRecoveryCode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []repo.TOTPRecoveryCode
	for _, c := range m.recovery[userID] {
		if c.used {
			continue
		}
		out = append(out, repo.TOTPRecoveryCode{ID: c.id, CodeHash: c.hash})
	}
	return out, nil
}

func (m *memAuthUsers) ClearTOTP(_ context.Context, userID int64, recoveryID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.userLocked(userID)
	if u == nil {
		return repo.ErrNotFound
	}
	if recoveryID > 0 {
		codes := m.recovery[userID]
		found := false
		for i := range codes {
			if codes[i].id == recoveryID && !codes[i].used {
				codes[i].used = true
				found = true
				break
			}
		}
		if m.recovery == nil {
			m.recovery = map[int64][]memRecoveryCode{}
		}
		m.recovery[userID] = codes
		if !found {
			return repo.ErrRecoveryCodeUsed
		}
	}
	if u.MFAType != "totp" {
		return repo.ErrNotFound
	}
	u.MFAEnabled = false
	u.MFAType = "none"
	u.MFAFactorActivated = false
	u.MFASecretCipher = ""
	u.MFATotpPending = false
	u.MFATotpLastStep = nil
	delete(m.recovery, userID)
	return nil
}

func (m *memAuthUsers) AcceptTOTPStep(_ context.Context, userID int64, step int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.userLocked(userID)
	if u == nil {
		return false, repo.ErrNotFound
	}
	if u.MFATotpLastStep != nil && *u.MFATotpLastStep >= step {
		return false, nil
	}
	stepCopy := step
	u.MFATotpLastStep = &stepCopy
	return true, nil
}

func (m *memAuthUsers) snapshot(username string) (model.User, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.byName[username]
	if u == nil {
		return model.User{}, false
	}
	cp := *u
	return cp, true
}
