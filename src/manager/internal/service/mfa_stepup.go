package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
	"bastion.local/shared/auth"
	smsutil "bastion.local/shared/sms"
)

const (
	cfgRequireAssignedFactor = "mfa.login.require_assigned_factor"
	cfgPrivilegedUKeyEnforce = "mfa.privileged.ukey_enforce"
	cfgStepUpTTLSeconds      = "mfa.stepup.ttl_seconds"

	stepUpKindProof   = "proof"
	stepUpKindPending = "pending"
	stepUpSceneSMS    = "mfa_stepup"
)

// 因子变更被拒绝时的稳定原因码。审计只写这些码，不写口令或验证码。
const (
	reasonBadPassword       = "bad_password"
	reasonFactorMismatch    = "factor_mismatch"
	reasonMissingStepUp     = "missing_step_up"
	reasonStaleOrReplay     = "stale_or_replayed_proof"
	reasonWrongTarget       = "wrong_target"
	reasonWrongAction       = "wrong_action"
	reasonPrivilegedDenied  = "privileged_ukey_required"
	reasonFactorNotAssigned = "factor_not_assigned"
	reasonTOTPNotEnrolled   = "totp_not_enrolled"
	reasonBootstrapPassword = "bootstrap_password"
	reasonPolicyDenied      = "policy_denied"
)

var (
	// ErrStepUpRejected 身份核验缺失、过期、用过，或与操作者、目标、动作不一致。
	ErrStepUpRejected = errors.New("身份核验无效或已使用，请重新验证")
	// ErrFactorRequired 账号还没有指定第二因子，而策略已经要求必须指定。
	ErrFactorRequired = errors.New("该账号尚未指定第二因子，不能只凭口令登录")
	// ErrPrivilegedUKeyRequired 管理员档账号在约束启用后必须使用 UKey。
	ErrPrivilegedUKeyRequired = errors.New("管理员账号必须使用 UKey 登录")
	// ErrTOTPNotEnrolled 已指定动态令牌但尚未完成绑定。网关不能因此只凭口令进入。
	ErrTOTPNotEnrolled = errors.New("动态令牌尚未完成绑定，不能只凭口令登录")
)

// stepUpRecord 是内存或数据库中的一条核验。
type stepUpRecord struct {
	Hash     string
	ActorID  int64
	TargetID int64
	Action   string
	Factor   string
	Kind     string
	Expires  time.Time
	Used     bool
}

// stepUpStore 保存一次性核验。Consume 必须是原子的。
type stepUpStore interface {
	Insert(ctx context.Context, row stepUpRecord) error
	ConsumeProof(ctx context.Context, hash string, actor, target int64, action, factor string) error
	ConsumePending(ctx context.Context, hash string) (stepUpRecord, error)
}

// mfaAuditor 写入因子变更审计。失败要让变更停下来。
type mfaAuditor interface {
	Append(ctx context.Context, ev repo.MFAAuditEvent) error
}

type memoryStepUp struct {
	mu   sync.Mutex
	rows map[string]stepUpRecord
}

func newMemoryStepUp() *memoryStepUp {
	return &memoryStepUp{rows: map[string]stepUpRecord{}}
}

func (m *memoryStepUp) Insert(_ context.Context, row stepUpRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rows[row.Hash]; ok {
		return fmt.Errorf("身份核验冲突")
	}
	m.rows[row.Hash] = row
	return nil
}

func (m *memoryStepUp) ConsumeProof(_ context.Context, hash string, actor, target int64, action, factor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[hash]
	if !ok || row.Used || row.Kind != stepUpKindProof || time.Now().After(row.Expires) {
		return repo.ErrStepUpRejected
	}
	if row.ActorID != actor || row.TargetID != target || row.Action != action || row.Factor != factor {
		return repo.ErrStepUpRejected
	}
	row.Used = true
	m.rows[hash] = row
	return nil
}

func (m *memoryStepUp) commit(hash string, actor, target int64, action, factor string, fn func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[hash]
	if !ok || row.Used || row.Kind != stepUpKindProof || time.Now().After(row.Expires) {
		return repo.ErrStepUpRejected
	}
	if row.ActorID != actor || row.TargetID != target || row.Action != action || row.Factor != factor {
		return repo.ErrStepUpRejected
	}
	if err := fn(); err != nil {
		return err
	}
	row.Used = true
	m.rows[hash] = row
	return nil
}

func (m *memoryStepUp) ConsumePending(_ context.Context, hash string) (stepUpRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[hash]
	if !ok || row.Used || row.Kind != stepUpKindPending || time.Now().After(row.Expires) {
		return stepUpRecord{}, repo.ErrStepUpRejected
	}
	row.Used = true
	m.rows[hash] = row
	return row, nil
}

type memoryAudit struct {
	mu    sync.Mutex
	items []repo.MFAAuditEvent
	fail  error
}

func (m *memoryAudit) Append(_ context.Context, ev repo.MFAAuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	m.items = append(m.items, ev)
	return nil
}

func (m *memoryAudit) snapshot() []repo.MFAAuditEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]repo.MFAAuditEvent, len(m.items))
	copy(out, m.items)
	return out
}

type dbStepUpStore struct{ store *repo.MFAPolicyStore }

func (d dbStepUpStore) Insert(ctx context.Context, row stepUpRecord) error {
	return d.store.InsertStepUp(ctx, repo.MFAStepUp{
		ProofHash: row.Hash, ActorID: row.ActorID, TargetID: row.TargetID,
		Action: row.Action, Factor: row.Factor, Kind: row.Kind, ExpiresAt: row.Expires,
	})
}

func (d dbStepUpStore) ConsumeProof(ctx context.Context, hash string, actor, target int64, action, factor string) error {
	return d.store.ConsumeStepUp(ctx, hash, actor, target, action, factor)
}

func (d dbStepUpStore) ConsumePending(ctx context.Context, hash string) (stepUpRecord, error) {
	row, err := d.store.ConsumePending(ctx, hash)
	if err != nil {
		return stepUpRecord{}, err
	}
	return stepUpRecord{
		Hash: hash, ActorID: row.ActorID, TargetID: row.TargetID,
		Action: row.Action, Factor: row.Factor, Kind: row.Kind, Expires: row.ExpiresAt,
	}, nil
}

type dbAuditor struct{ store *repo.MFAPolicyStore }

func (d dbAuditor) Append(ctx context.Context, ev repo.MFAAuditEvent) error {
	return d.store.AppendAudit(ctx, ev)
}

// SetMFAPolicyStore 使用数据库保存核验和审计，避免进程重启后证明还能用。
func (s *AuthService) SetMFAPolicyStore(store *repo.MFAPolicyStore) {
	if s == nil || store == nil {
		return
	}
	s.stepUps = dbStepUpStore{store: store}
	s.mfaAudit = dbAuditor{store: store}
	s.mfaReady = store
}

func (s *AuthService) ensureStepUp() stepUpStore {
	if s.stepUps == nil {
		s.stepUps = newMemoryStepUp()
	}
	return s.stepUps
}

func (s *AuthService) auditMFA(ctx context.Context, ev repo.MFAAuditEvent) error {
	ev.Reason = sanitizeAuditReason(ev.Reason)
	ev.OldMethod = auth.NormalizeFactor(ev.OldMethod)
	if ev.NewMethod != "" {
		ev.NewMethod = auth.NormalizeFactor(ev.NewMethod)
	}
	if ev.Result != "success" && ev.Result != "denied" {
		ev.Result = "denied"
	}
	if s == nil || s.mfaAudit == nil {
		return fmt.Errorf("因子变更审计未启用")
	}
	return s.mfaAudit.Append(ctx, ev)
}

func sanitizeAuditReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ""
	}
	for _, r := range reason {
		if r > 127 || r == ' ' {
			return reasonPolicyDenied
		}
	}
	if len(reason) > 64 {
		return reason[:64]
	}
	return reason
}

func proofFactor(factor string) string {
	factor = auth.NormalizeFactor(factor)
	if factor == auth.FactorNone {
		return ""
	}
	return factor
}

func hashStepUp(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func newStepUpSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *AuthService) stepUpTTL(ctx context.Context) time.Duration {
	sec := s.cfgInt(ctx, cfgStepUpTTLSeconds, 300)
	if sec < 30 {
		sec = 30
	}
	if sec > 900 {
		sec = 900
	}
	return time.Duration(sec) * time.Second
}

func (s *AuthService) requireAssignedFactor(ctx context.Context) bool {
	return s.cfgBool(ctx, cfgRequireAssignedFactor, false)
}

func (s *AuthService) privilegedUKeyEnforce(ctx context.Context) bool {
	return s.cfgBool(ctx, cfgPrivilegedUKeyEnforce, false)
}

func (s *AuthService) userPrivileged(ctx context.Context, userID int64) (bool, error) {
	if s == nil || s.userRepo == nil || userID == 0 {
		return false, nil
	}
	roles, err := s.userRepo.ListRoles(ctx, userID)
	if err != nil {
		return false, err
	}
	level := 0
	if s.roleRepo != nil {
		level, err = s.roleRepo.LevelOfUser(ctx, userID)
		if err != nil {
			return false, err
		}
	}
	return auth.IsPrivileged(roles, level), nil
}

func (s *AuthService) issueProof(ctx context.Context, actor, target int64, action, factor string) (string, time.Time, error) {
	raw, err := newStepUpSecret()
	if err != nil {
		return "", time.Time{}, err
	}
	exp := time.Now().Add(s.stepUpTTL(ctx))
	err = s.ensureStepUp().Insert(ctx, stepUpRecord{
		Hash: hashStepUp(raw), ActorID: actor, TargetID: target,
		Action: action, Factor: proofFactor(factor), Kind: stepUpKindProof, Expires: exp,
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return raw, exp, nil
}

// ConsumeFactorProof 消费一条与操作者、目标、动作和因子都一致的证明。
func (s *AuthService) ConsumeFactorProof(ctx context.Context, raw string, actor, target int64, action, factor string) error {
	return s.CommitWithProof(ctx, raw, actor, target, action, factor, func(pgx.Tx) error { return nil })
}

// CommitWithProof 在证明仍有效时执行 mutate。mutate 失败则证明不被消费。
// 数据库实现里，消费与 mutate 在同一个事务中，因此不会先记下成功再写失败。
func (s *AuthService) CommitWithProof(ctx context.Context, raw string, actor, target int64, action, factor string, mutate func(tx pgx.Tx) error) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || mutate == nil {
		return ErrStepUpRejected
	}
	factor = proofFactor(factor)
	sum := hashStepUp(raw)
	if db, ok := s.stepUps.(dbStepUpStore); ok {
		return mapStepUpErr(db.store.ConsumeAnd(ctx, sum, actor, target, action, factor, mutate))
	}
	mem, ok := s.ensureStepUp().(*memoryStepUp)
	if !ok {
		return ErrStepUpRejected
	}
	return mapStepUpErr(mem.commit(sum, actor, target, action, factor, func() error { return mutate(nil) }))
}

func mapStepUpErr(err error) error {
	if err == nil || errors.Is(err, ErrStepUpRejected) {
		return err
	}
	if errors.Is(err, repo.ErrStepUpRejected) {
		return ErrStepUpRejected
	}
	return err
}

// StepUpRequest 是管理员或用户为自己发起的新鲜身份核验。
//
// 访问令牌只用来知道是谁在调用。口令和该账号当前的因子必须在这次请求里重新验证。
type StepUpRequest struct {
	ActorID      int64  `json:"-"`
	Password     string `json:"password"`
	TOTPCode     string `json:"totp_code"`
	SMSCode      string `json:"sms_code"`
	ChallengeID  string `json:"challenge_id"`
	Cert         string `json:"cert"`
	SrcData      string `json:"src_data"`
	Signature    string `json:"signature"`
	Action       string `json:"action"`
	TargetUserID int64  `json:"target_user_id"`
	NewFactor    string `json:"new_factor"`
	ClientIP     string `json:"-"`
}

// StepUpResult 是核验结果。Proof 只在因子已经验证通过后出现，且只能使用一次。
type StepUpResult struct {
	Proof       string    `json:"step_up_proof,omitempty"`
	Expires     time.Time `json:"step_up_expires,omitempty"`
	Action      string    `json:"action,omitempty"`
	MFARequired bool      `json:"mfa_required,omitempty"`
	MFAType     string    `json:"mfa_type,omitempty"`
	ChallengeID string    `json:"challenge_id,omitempty"`
	SignData    string    `json:"sign_data,omitempty"`
	SrcData     string    `json:"src_data,omitempty"`
	PhoneMasked string    `json:"phone_masked,omitempty"`
	MFAHint     string    `json:"mfa_hint,omitempty"`
}

// BeginStepUp 验证操作者本人的口令和当前因子，然后签发一次性证明。
//
// 不验证目标账号的因子。管理员不需要知道对方的验证码或 UKey。
func (s *AuthService) BeginStepUp(ctx context.Context, req StepUpRequest) (*StepUpResult, error) {
	if s == nil || s.userRepo == nil {
		return nil, fmt.Errorf("身份核验未启用")
	}
	actor, err := s.userRepo.GetByID(ctx, req.ActorID)
	if err != nil || actor == nil {
		return nil, ErrStepUpRejected
	}
	auditBase := repo.MFAAuditEvent{
		ActorID: actor.UserID, ActorName: actor.Username,
		TargetID: req.TargetUserID, Action: req.Action,
		OldMethod: auth.NormalizeFactor(actor.MFAType), NewMethod: auth.NormalizeFactor(req.NewFactor),
		Result: "denied", ClientIP: req.ClientIP,
	}
	deny := func(reason string) error {
		auditBase.Reason = reason
		if aerr := s.auditMFA(ctx, auditBase); aerr != nil {
			return fmt.Errorf("身份核验被拒绝，且审计写入失败: %w", aerr)
		}
		return ErrStepUpRejected
	}
	if !allowedStepUpAction(req.Action) {
		return nil, deny(reasonWrongAction)
	}
	if strings.TrimSpace(req.Password) == "" || !auth.VerifyPassword(req.Password, actor.PasswordHash, actor.PasswordSalt) {
		_ = s.onLoginFail(ctx, actor, s.cfgInt(ctx, "security.login.max_fail", 5), s.cfgInt(ctx, "security.login.lock_minutes", 15))
		return nil, deny(reasonBadPassword)
	}
	if req.Action == auth.StepUpTOTPEnroll {
		if req.TargetUserID != actor.UserID || !auth.TOTPPasswordEnroll(factorState(actor)) {
			return nil, deny("totp_rebind_blocked")
		}
		raw, exp, err := s.issueProof(ctx, actor.UserID, actor.UserID, auth.StepUpTOTPEnroll, auth.FactorTOTP)
		if err != nil {
			return nil, err
		}
		return &StepUpResult{Proof: raw, Expires: exp, Action: auth.StepUpTOTPEnroll}, nil
	}
	switch auth.NormalizeFactor(actor.MFAType) {
	case auth.FactorTOTP:
		if !totpView(actor).LoginRequiresTOTP() {
			return nil, deny("totp_unavailable")
		}
		if strings.TrimSpace(req.TOTPCode) == "" {
			return nil, deny(reasonFactorMismatch)
		}
		secret, err := s.crypto.DecryptMFA(ctx, actor.MFASecretCipher)
		if err != nil {
			return nil, err
		}
		ok, step := auth.MatchTOTP(secret, req.TOTPCode, auth.TOTPAlgoSHA1, time.Now())
		if !ok {
			return nil, deny(reasonFactorMismatch)
		}
		accepted, err := s.userRepo.AcceptTOTPStep(ctx, actor.UserID, step)
		if err != nil {
			return nil, err
		}
		if !accepted {
			return nil, deny(reasonStaleOrReplay)
		}
		return s.finishStepUpProof(ctx, actor, req)
	case auth.FactorSMS:
		if !smsutil.ValidPhone(smsutil.NormalizePhone(actor.Phone)) {
			return nil, deny("factor_unavailable")
		}
		if strings.TrimSpace(req.SMSCode) == "" {
			return s.startSMSStepUp(ctx, actor, req)
		}
		return s.finishSMSStepUp(ctx, actor, req)
	case auth.FactorUKey:
		if strings.TrimSpace(req.Signature) == "" {
			return s.startUKeyStepUp(ctx, actor, req)
		}
		return s.finishUKeyStepUp(ctx, actor, req)
	default:
		ok, why := auth.BootstrapEligible(factorState(actor))
		if !ok {
			return nil, deny(why)
		}
		if s.requireAssignedFactor(ctx) {
			return nil, deny(reasonFactorNotAssigned)
		}
		return s.bootstrapStepUp(ctx, actor, req, auditBase)
	}
}

func factorState(u *model.User) auth.FactorState {
	if u == nil {
		return auth.FactorState{}
	}
	return auth.FactorState{
		Type: u.MFAType, Enabled: u.MFAEnabled,
		HasSecret: strings.TrimSpace(u.MFASecretCipher) != "",
		Pending:   u.MFATotpPending, Activated: u.MFAFactorActivated,
	}
}

func allowedStepUpAction(action string) bool {
	switch action {
	case auth.StepUpTOTPEnroll, auth.StepUpMFAAssign, auth.StepUpMFAPhone,
		auth.StepUpGrantPrivileged, auth.StepUpUKeyBind, auth.StepUpUKeyRevoke, auth.StepUpUKeyCARevoke,
		auth.StepUpEnforcement, auth.StepUpTOTPReopen:
		return true
	default:
		return false
	}
}

func (s *AuthService) bootstrapStepUp(ctx context.Context, actor *model.User, req StepUpRequest, auditBase repo.MFAAuditEvent) (*StepUpResult, error) {
	raw, exp, err := s.issueProof(ctx, actor.UserID, req.TargetUserID, req.Action, req.NewFactor)
	if err != nil {
		return nil, err
	}
	auditBase.Result = "success"
	auditBase.Reason = reasonBootstrapPassword
	if err := s.auditMFA(ctx, auditBase); err != nil {
		return nil, err
	}
	return &StepUpResult{Proof: raw, Expires: exp, Action: req.Action, MFAHint: "当前账号还没有可用的第二因子，本次仅以口令核验并已记入审计"}, nil
}

func (s *AuthService) finishStepUpProof(ctx context.Context, actor *model.User, req StepUpRequest) (*StepUpResult, error) {
	factor := req.NewFactor
	if req.Action == auth.StepUpUKeyBind || req.Action == auth.StepUpUKeyRevoke || req.Action == auth.StepUpUKeyCARevoke {
		factor = ""
	}
	if req.Action == auth.StepUpMFAPhone {
		factor = auth.FactorSMS
	}
	raw, exp, err := s.issueProof(ctx, actor.UserID, req.TargetUserID, req.Action, factor)
	if err != nil {
		return nil, err
	}
	if err := s.auditMFA(ctx, repo.MFAAuditEvent{
		ActorID: actor.UserID, ActorName: actor.Username, TargetID: req.TargetUserID,
		Action: req.Action, OldMethod: auth.NormalizeFactor(actor.MFAType),
		NewMethod: auth.NormalizeFactor(factor), Result: "success", Reason: "step_up_issued",
		ClientIP: req.ClientIP,
	}); err != nil {
		return nil, err
	}
	return &StepUpResult{Proof: raw, Expires: exp, Action: req.Action}, nil
}

func (s *AuthService) startSMSStepUp(ctx context.Context, actor *model.User, req StepUpRequest) (*StepUpResult, error) {
	if s.sms == nil {
		return nil, fmt.Errorf("短信服务未启用，无法核验当前因子")
	}
	phone := smsutil.NormalizePhone(actor.Phone)
	if !smsutil.ValidPhone(phone) {
		return nil, fmt.Errorf("当前账号没有可用的手机号，无法完成短信核验")
	}
	raw, err := newStepUpSecret()
	if err != nil {
		return nil, err
	}
	exp := time.Now().Add(s.stepUpTTL(ctx))
	if err := s.ensureStepUp().Insert(ctx, stepUpRecord{
		Hash: hashStepUp(raw), ActorID: actor.UserID, TargetID: req.TargetUserID,
		Action: req.Action, Factor: proofFactor(req.NewFactor), Kind: stepUpKindPending, Expires: exp,
	}); err != nil {
		return nil, err
	}
	if _, err := s.sms.Send(ctx, phone, stepUpSceneSMS, req.ClientIP, raw); err != nil {
		return nil, err
	}
	return &StepUpResult{
		MFARequired: true, MFAType: auth.FactorSMS, ChallengeID: raw,
		PhoneMasked: smsutil.MaskPhone(phone),
		MFAHint:     "验证码已发往当前账号的手机，不是目标用户的手机",
	}, nil
}

func (s *AuthService) finishSMSStepUp(ctx context.Context, actor *model.User, req StepUpRequest) (*StepUpResult, error) {
	pending, err := s.ensureStepUp().ConsumePending(ctx, hashStepUp(req.ChallengeID))
	if err != nil || pending.ActorID != actor.UserID || pending.Action != req.Action || pending.TargetID != req.TargetUserID {
		_ = s.auditMFA(ctx, repo.MFAAuditEvent{
			ActorID: actor.UserID, ActorName: actor.Username, TargetID: req.TargetUserID,
			Action: req.Action, Result: "denied", Reason: reasonStaleOrReplay, ClientIP: req.ClientIP,
		})
		return nil, ErrStepUpRejected
	}
	if err := s.sms.Verify(ctx, smsutil.NormalizePhone(actor.Phone), req.SMSCode, stepUpSceneSMS, req.ChallengeID); err != nil {
		_ = s.auditMFA(ctx, repo.MFAAuditEvent{
			ActorID: actor.UserID, ActorName: actor.Username, TargetID: req.TargetUserID,
			Action: req.Action, Result: "denied", Reason: reasonFactorMismatch, ClientIP: req.ClientIP,
		})
		return nil, ErrStepUpRejected
	}
	req.NewFactor = pending.Factor
	return s.finishStepUpProof(ctx, actor, req)
}

func (s *AuthService) startUKeyStepUp(ctx context.Context, actor *model.User, req StepUpRequest) (*StepUpResult, error) {
	if s.ukey == nil {
		return nil, fmt.Errorf("UKey 模块未启用，无法核验当前因子")
	}
	ch, err := s.ukey.IssueStepUpChallenge(ctx, actor, req.ClientIP, "")
	if err != nil {
		return nil, err
	}
	exp := ch.ExpireTime
	if err := s.ensureStepUp().Insert(ctx, stepUpRecord{
		Hash: hashStepUp(ch.ChallengeID), ActorID: actor.UserID, TargetID: req.TargetUserID,
		Action: req.Action, Factor: proofFactor(req.NewFactor), Kind: stepUpKindPending, Expires: exp,
	}); err != nil {
		return nil, err
	}
	return &StepUpResult{
		MFARequired: true, MFAType: auth.FactorUKey, ChallengeID: ch.ChallengeID,
		SignData: ch.SignData, SrcData: ch.SrcData,
		MFAHint: "请使用当前账号的 UKey 签名，不要使用目标用户的 UKey",
	}, nil
}

func (s *AuthService) finishUKeyStepUp(ctx context.Context, actor *model.User, req StepUpRequest) (*StepUpResult, error) {
	pending, err := s.ensureStepUp().ConsumePending(ctx, hashStepUp(req.ChallengeID))
	if err != nil || pending.ActorID != actor.UserID || pending.Action != req.Action || pending.TargetID != req.TargetUserID {
		_ = s.auditMFA(ctx, repo.MFAAuditEvent{
			ActorID: actor.UserID, ActorName: actor.Username, TargetID: req.TargetUserID,
			Action: req.Action, Result: "denied", Reason: reasonWrongTarget, ClientIP: req.ClientIP,
		})
		return nil, ErrStepUpRejected
	}
	if _, _, err := s.ukey.VerifyStepUp(ctx, UKeyLoginRequest{
		ChallengeID: req.ChallengeID, Cert: req.Cert, SrcData: req.SrcData, Signature: req.Signature,
	}, req.ClientIP); err != nil {
		_ = s.auditMFA(ctx, repo.MFAAuditEvent{
			ActorID: actor.UserID, ActorName: actor.Username, TargetID: req.TargetUserID,
			Action: req.Action, Result: "denied", Reason: reasonFactorMismatch, ClientIP: req.ClientIP,
		})
		return nil, ErrStepUpRejected
	}
	req.NewFactor = pending.Factor
	return s.finishStepUpProof(ctx, actor, req)
}

// CommitFactorChange 把证书写入放进证明事务。写入失败时证明仍可再用。
func (s *AuthService) CommitFactorChange(ctx context.Context, actor, target int64, action, proof string, mutate func(tx pgx.Tx) error) error {
	if err := s.CommitWithProof(ctx, proof, actor, target, action, "", mutate); err != nil {
		if errors.Is(err, ErrStepUpRejected) {
			_ = s.auditMFA(ctx, repo.MFAAuditEvent{
				ActorID: actor, TargetID: target, Action: action,
				Result: "denied", Reason: reasonMissingStepUp,
			})
		}
		return err
	}
	return nil
}

// AuthorizeFactorChange 供只需要消费证明、没有额外写入的调用保留。
func (s *AuthService) AuthorizeFactorChange(ctx context.Context, actor, target int64, action, proof string) error {
	if err := s.ConsumeFactorProof(ctx, proof, actor, target, action, ""); err != nil {
		_ = s.auditMFA(ctx, repo.MFAAuditEvent{
			ActorID: actor, TargetID: target, Action: action,
			Result: "denied", Reason: reasonMissingStepUp,
		})
		return err
	}
	return nil
}

// PrivilegedCertRevokeBlocked 拒绝吊销会让管理员失去 UKey 的最后一张证书。
//
// 约束开启时，任何管理员档 UKey 账号都不能失去最后一张有效证书。
// 约束关闭时，只拦住“最后一名仍能登录的管理员”。
func (s *AuthService) PrivilegedCertRevokeBlocked(ctx context.Context, userID int64) (bool, error) {
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil || u == nil {
		return false, err
	}
	if auth.NormalizeFactor(u.MFAType) != auth.FactorUKey {
		return false, nil
	}
	priv, err := s.userPrivileged(ctx, userID)
	if err != nil || !priv {
		return false, err
	}
	if s.privilegedUKeyEnforce(ctx) {
		return true, nil
	}
	if s.mfaReady == nil {
		return false, nil
	}
	others, err := s.userRepoCountPrivileged(ctx, userID)
	if err != nil {
		return false, err
	}
	return others == 0, nil
}

func (s *AuthService) userRepoCountPrivileged(ctx context.Context, exclude int64) (int, error) {
	counter, ok := s.userRepo.(interface {
		CountUsablePrivilegedExcept(ctx context.Context, excludeID int64, requireAssigned bool) (int, error)
	})
	if !ok {
		return 1, nil
	}
	return counter.CountUsablePrivilegedExcept(ctx, exclude, s.requireAssignedFactor(ctx))
}
