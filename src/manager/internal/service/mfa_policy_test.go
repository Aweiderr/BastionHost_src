package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
	"bastion.local/shared/auth"
)

func TestLoginRejectsWrongFactorAndPrivilegedDowngrade(t *testing.T) {
	users := &memAuthUsers{}
	smsUser := hashedUser(t, 51, "sms-only", "sms", true)
	ukeyUser := hashedUser(t, 52, "ukey-only", "ukey", true)
	totpUser := hashedUser(t, 53, "totp-only", "totp", true)
	helper, secret := newMFACrypto(t)
	cipher, err := helper.EncryptMFA(context.Background(), secret)
	if err != nil {
		t.Fatal(err)
	}
	totpUser.MFASecretCipher = cipher
	noneUser := hashedUser(t, 54, "legacy-none", "none", false)
	admin := hashedUser(t, 55, "sysadmin", "totp", true)
	admin.MFASecretCipher = cipher
	users.add(smsUser)
	users.add(ukeyUser)
	users.add(totpUser)
	users.add(noneUser)
	users.add(admin)
	users.setRoles(55, []string{"sys_admin"})
	svc, _, _ := newTOTPAuth(t, users)
	svc.crypto = helper
	svc.configRepo = boolConfig{
		"captcha.enabled":        false,
		cfgPrivilegedUKeyEnforce: true,
		cfgRequireAssignedFactor: true,
	}
	ctx := context.Background()

	if _, err := svc.Login(ctx, LoginRequest{Username: "sms-only", Password: gatewayTestPassword, TOTPCode: "123456"}); err == nil || !strings.Contains(err.Error(), "短信") {
		t.Fatalf("短信账号不能改用动态令牌登录: %v", err)
	}
	if _, err := svc.Login(ctx, LoginRequest{Username: "ukey-only", Password: gatewayTestPassword, TOTPCode: "123456"}); err == nil || !strings.Contains(err.Error(), "UKey") {
		t.Fatalf("UKey 账号不能改用动态令牌登录: %v", err)
	}
	code := auth.GenerateTOTP(secret, time.Now(), auth.TOTPAlgoSHA1)
	if _, _, _, err := svc.Authenticate(ctx, "totp-only", gatewayTestPassword, code, "10.9.0.1"); err != nil {
		t.Fatalf("运维人员的动态令牌登录应保持可用: %v", err)
	}
	if _, err := svc.Login(ctx, LoginRequest{Username: "legacy-none", Password: gatewayTestPassword}); !errors.Is(err, ErrFactorRequired) {
		t.Fatalf("要求指定因子后，none 不能只凭口令登录: %v", err)
	}
	if _, _, _, err := svc.Authenticate(ctx, "sysadmin", gatewayTestPassword, code, "10.9.0.2"); !errors.Is(err, ErrPrivilegedUKeyRequired) {
		t.Fatalf("约束启用后管理员不能继续用动态令牌: %v", err)
	}

	svc.configRepo = boolConfig{"captcha.enabled": false}
	res, err := svc.Login(ctx, LoginRequest{Username: "sysadmin", Password: gatewayTestPassword, TOTPCode: auth.GenerateTOTP(secret, time.Now(), auth.TOTPAlgoSHA1)})
	if err != nil || res.AccessToken == "" {
		t.Fatalf("约束关闭时现有管理员应仍能用原来的因子: %v", err)
	}
	if _, err := svc.Login(ctx, LoginRequest{Username: "legacy-none", Password: gatewayTestPassword}); err != nil {
		t.Fatalf("开关关闭时，尚未指定因子的旧账号仍可口令登录: %v", err)
	}
}

func TestStepUpRejectsReplayWrongTargetAndStale(t *testing.T) {
	users := &memAuthUsers{}
	actor := hashedUser(t, 61, "actor", "none", false)
	users.add(actor)
	svc, _, _ := newTOTPAuth(t, users)
	ctx := context.Background()
	res, err := svc.BeginStepUp(ctx, StepUpRequest{
		ActorID: actor.UserID, Password: gatewayTestPassword,
		Action: auth.StepUpMFAAssign, TargetUserID: 77, NewFactor: auth.FactorUKey,
	})
	if err != nil || res.Proof == "" {
		t.Fatalf("尚未配置因子的管理员应以口令核验并留下证明: %v", err)
	}
	audits := svc.mfaAudit.(*memoryAudit).snapshot()
	if len(audits) == 0 || audits[len(audits)-1].Reason != reasonBootstrapPassword || audits[len(audits)-1].Result != "success" {
		t.Fatalf("引导核验应记成功审计: %+v", audits)
	}
	if err := svc.ConsumeFactorProof(ctx, res.Proof, actor.UserID, 78, auth.StepUpMFAAssign, auth.FactorUKey); !errors.Is(err, ErrStepUpRejected) {
		t.Fatalf("证明不能用于别的目标: %v", err)
	}
	if err := svc.ConsumeFactorProof(ctx, res.Proof, actor.UserID, 77, auth.StepUpUKeyBind, ""); !errors.Is(err, ErrStepUpRejected) {
		t.Fatalf("证明不能改作别的动作: %v", err)
	}
	if err := svc.ConsumeFactorProof(ctx, res.Proof, actor.UserID, 77, auth.StepUpMFAAssign, auth.FactorTOTP); !errors.Is(err, ErrStepUpRejected) {
		t.Fatalf("证明不能改成别的因子: %v", err)
	}
	if err := svc.ConsumeFactorProof(ctx, res.Proof, actor.UserID, 77, auth.StepUpMFAAssign, auth.FactorUKey); err != nil {
		t.Fatal(err)
	}
	if err := svc.ConsumeFactorProof(ctx, res.Proof, actor.UserID, 77, auth.StepUpMFAAssign, auth.FactorUKey); !errors.Is(err, ErrStepUpRejected) {
		t.Fatalf("证明不能重放: %v", err)
	}

	store := svc.ensureStepUp().(*memoryStepUp)
	raw := "expired-proof"
	store.rows[hashStepUp(raw)] = stepUpRecord{
		Hash: hashStepUp(raw), ActorID: actor.UserID, TargetID: 77,
		Action: auth.StepUpMFAAssign, Factor: auth.FactorUKey, Kind: stepUpKindProof,
		Expires: time.Now().Add(-time.Minute),
	}
	if err := svc.ConsumeFactorProof(ctx, raw, actor.UserID, 77, auth.StepUpMFAAssign, auth.FactorUKey); !errors.Is(err, ErrStepUpRejected) {
		t.Fatalf("过期证明应拒绝: %v", err)
	}
	if _, err := svc.BeginStepUp(ctx, StepUpRequest{
		ActorID: actor.UserID, Password: "wrong", Action: auth.StepUpMFAAssign, TargetUserID: 77, NewFactor: auth.FactorSMS,
	}); !errors.Is(err, ErrStepUpRejected) {
		t.Fatalf("口令错误不能签发证明: %v", err)
	}
}

func TestPasswordBootstrapRejectsAssignedButUnusableFactors(t *testing.T) {
	secretUsers := func(id int64, name, factor string, enabled bool) *model.User {
		u := hashedUser(t, id, name, factor, enabled)
		return u
	}
	ctx := context.Background()

	t.Run("never enrolled", func(t *testing.T) {
		users := &memAuthUsers{}
		users.add(secretUsers(1, "fresh", "none", false))
		svc, _, _ := newTOTPAuth(t, users)
		res, err := svc.BeginStepUp(ctx, StepUpRequest{
			ActorID: 1, Password: gatewayTestPassword, Action: auth.StepUpMFAAssign,
			TargetUserID: 9, NewFactor: auth.FactorTOTP,
		})
		if err != nil || res.Proof == "" {
			t.Fatal(err)
		}
	})
	t.Run("totp not confirmed cannot reassign", func(t *testing.T) {
		users := &memAuthUsers{}
		u := secretUsers(2, "preset", "totp", false)
		users.add(u)
		svc, _, _ := newTOTPAuth(t, users)
		if _, err := svc.BeginStepUp(ctx, StepUpRequest{
			ActorID: 2, Password: gatewayTestPassword, Action: auth.StepUpMFAAssign,
			TargetUserID: 2, NewFactor: auth.FactorSMS,
		}); err == nil {
			t.Fatal("未确认的动态令牌不能只凭口令改因子")
		}
		res, err := svc.BeginStepUp(ctx, StepUpRequest{
			ActorID: 2, Password: gatewayTestPassword, Action: auth.StepUpTOTPEnroll, TargetUserID: 2,
		})
		if err != nil || res.Proof == "" {
			t.Fatalf("初次绑定应允许口令证明: %v", err)
		}
	})
	t.Run("confirmed totp then cleared", func(t *testing.T) {
		users := &memAuthUsers{}
		u := secretUsers(3, "was", "totp", false)
		u.MFAFactorActivated = true
		users.add(u)
		svc, _, _ := newTOTPAuth(t, users)
		if _, err := svc.BeginStepUp(ctx, StepUpRequest{
			ActorID: 3, Password: gatewayTestPassword, Action: auth.StepUpTOTPEnroll, TargetUserID: 3,
		}); err == nil {
			t.Fatal("曾生效的动态令牌不能只凭口令重绑")
		}
	})
	t.Run("sms without phone", func(t *testing.T) {
		users := &memAuthUsers{}
		u := secretUsers(4, "sms", "sms", true)
		u.Phone = ""
		users.add(u)
		svc, _, _ := newTOTPAuth(t, users)
		if _, err := svc.BeginStepUp(ctx, StepUpRequest{
			ActorID: 4, Password: gatewayTestPassword, Action: auth.StepUpMFAAssign,
			TargetUserID: 4, NewFactor: auth.FactorTOTP,
		}); err == nil {
			t.Fatal("短信因子没有手机号时不能退回口令")
		}
	})
	t.Run("ukey without module", func(t *testing.T) {
		users := &memAuthUsers{}
		users.add(secretUsers(5, "ukey", "ukey", false))
		svc, _, _ := newTOTPAuth(t, users)
		if _, err := svc.BeginStepUp(ctx, StepUpRequest{
			ActorID: 5, Password: gatewayTestPassword, Action: auth.StepUpUKeyBind, TargetUserID: 8,
		}); err == nil {
			t.Fatal("已指定 UKey 时不能只凭口令核验")
		}
	})
	t.Run("inconsistent none", func(t *testing.T) {
		users := &memAuthUsers{}
		u := secretUsers(6, "dirty", "none", false)
		u.MFAFactorActivated = true
		users.add(u)
		svc, _, _ := newTOTPAuth(t, users)
		if _, err := svc.BeginStepUp(ctx, StepUpRequest{
			ActorID: 6, Password: gatewayTestPassword, Action: auth.StepUpMFAAssign,
			TargetUserID: 6, NewFactor: auth.FactorSMS,
		}); err == nil {
			t.Fatal("类型为空但曾生效的账号不能口令引导")
		}
	})
}

func TestProofNotConsumedWhenMutationFails(t *testing.T) {
	users := &memAuthUsers{}
	users.add(hashedUser(t, 8, "fresh", "none", false))
	svc, _, _ := newTOTPAuth(t, users)
	res, err := svc.BeginStepUp(context.Background(), StepUpRequest{
		ActorID: 8, Password: gatewayTestPassword, Action: auth.StepUpMFAAssign,
		TargetUserID: 1, NewFactor: auth.FactorUKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("state write failed")
	if err := svc.CommitWithProof(context.Background(), res.Proof, 8, 1, auth.StepUpMFAAssign, auth.FactorUKey, func(pgx.Tx) error {
		return boom
	}); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if err := svc.CommitWithProof(context.Background(), res.Proof, 8, 1, auth.StepUpMFAAssign, auth.FactorUKey, func(pgx.Tx) error {
		return nil
	}); err != nil {
		t.Fatalf("失败的写入不应消费证明: %v", err)
	}
}

func TestTOTPConfirmDoesNotSwitchFactor(t *testing.T) {
	users := &memAuthUsers{}
	u := hashedUser(t, 71, "sms-user", "sms", true)
	users.add(u)
	svc, _, _ := newTOTPAuth(t, users)
	ctx := context.Background()
	raw, _, err := svc.issueProof(ctx, u.UserID, u.UserID, auth.StepUpTOTPEnroll, auth.FactorTOTP)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartTOTPBinding(ctx, u.UserID, "Bastion", raw); err == nil {
		t.Fatal("短信账号不能靠绑定动态令牌改掉指定因子")
	}
	snap, _ := users.snapshot("sms-user")
	if snap.MFAType != "sms" || !snap.MFAEnabled {
		t.Fatalf("失败的绑定不能改因子: %+v", snap)
	}
}

func TestReadinessBlocksPrematureEnforcement(t *testing.T) {
	blockers := readinessBlockers(repo.ReadinessUser{
		UserID: 1, Username: "admin", Factor: "totp", MaxLevel: 80, RoleCodes: "sys_admin",
		MFAEnabled: true, HasSecret: true,
	})
	var sawPriv bool
	for _, b := range blockers {
		if b.Reason == "privileged_not_ukey" && b.BlocksPrivilegedUKey && !b.BlocksRequireAssigned {
			sawPriv = true
		}
	}
	if !sawPriv {
		t.Fatalf("已绑定动态令牌的管理员只应挡住 UKey 约束，不应挡住普通因子要求: %+v", blockers)
	}
	none := readinessBlockers(repo.ReadinessUser{UserID: 2, Username: "op", Factor: "none", MaxLevel: 40, RoleCodes: "operator"})
	if len(none) != 1 || !none[0].BlocksRequireAssigned || none[0].BlocksPrivilegedUKey {
		t.Fatalf("运维人员未指定因子只挡住“必须指定因子”: %+v", none)
	}
	ready := readinessBlockers(repo.ReadinessUser{
		UserID: 3, Username: "sec", Factor: "ukey", MaxLevel: 80, RoleCodes: "sec_admin", ActiveCert: 1, MFAEnabled: true,
	})
	if len(ready) != 0 {
		t.Fatalf("已有有效证书的 UKey 管理员不应成为阻断项: %+v", ready)
	}
}
