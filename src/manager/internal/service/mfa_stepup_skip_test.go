package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"bastion.local/shared/auth"
)

func TestCommitWithProofAllowsEmptyWhenNoActivatedFactor(t *testing.T) {
	users := &memAuthUsers{}
	actor := hashedUser(t, 201, "sysadmin", "none", false)
	users.add(actor)
	svc, _, _ := newTOTPAuth(t, users)
	ctx := context.Background()

	called := 0
	if err := svc.CommitWithProof(ctx, "", actor.UserID, 0, auth.StepUpMFAAssign, auth.FactorSMS, func(pgx.Tx) error {
		called++
		return nil
	}); err != nil || called != 1 {
		t.Fatalf("未激活因子时侯空证明应放行: called=%d err=%v", called, err)
	}

	// 残留 totp：类型还在，但种子已空，不能当成已激活因子。
	residual := hashedUser(t, 202, "leftover", "totp", false)
	residual.MFAFactorActivated = true
	residual.MFASecretCipher = ""
	residual.MFATotpPending = false
	users.add(residual)
	called = 0
	if err := svc.CommitWithProof(ctx, "", residual.UserID, 0, auth.StepUpMFAAssign, auth.FactorTOTP, func(pgx.Tx) error {
		called++
		return nil
	}); err != nil || called != 1 {
		t.Fatalf("残留 totp 空证明应放行: called=%d err=%v", called, err)
	}
}

func TestCommitWithProofRejectsEmptyWhenFactorActivated(t *testing.T) {
	users := &memAuthUsers{}
	actor := hashedUser(t, 203, "sysadmin", "totp", true)
	actor.MFAFactorActivated = true
	actor.MFASecretCipher = "cipher"
	actor.MFATotpPending = false
	users.add(actor)
	svc, _, _ := newTOTPAuth(t, users)
	ctx := context.Background()
	if err := svc.CommitWithProof(ctx, "", actor.UserID, 0, auth.StepUpMFAAssign, auth.FactorSMS, func(pgx.Tx) error {
		t.Fatal("已激活因子时不能空证明写入")
		return nil
	}); !errors.Is(err, ErrStepUpRejected) {
		t.Fatalf("已激活因子应拒绝空证明: %v", err)
	}
}

func TestBeginStepUpResidualTOTPDoesNotDemandCode(t *testing.T) {
	users := &memAuthUsers{}
	actor := hashedUser(t, 204, "sysadmin", "totp", false)
	actor.MFAFactorActivated = true
	actor.MFASecretCipher = ""
	users.add(actor)
	svc, _, _ := newTOTPAuth(t, users)
	res, err := svc.BeginStepUp(context.Background(), StepUpRequest{
		ActorID: actor.UserID, Password: gatewayTestPassword,
		Action: auth.StepUpMFAAssign, TargetUserID: 0,
	})
	if err != nil || res == nil || res.Proof == "" {
		t.Fatalf("残留 totp 应允许口令核验: %v", err)
	}
}

func TestStartTOTPBindingAfterUnbindAllowsEmptyProof(t *testing.T) {
	users := &memAuthUsers{}
	actor := hashedUser(t, 205, "sysadmin", "none", false)
	users.add(actor)
	svc, _, _ := newTOTPAuth(t, users)
	ctx := context.Background()
	binding, err := svc.StartTOTPBinding(ctx, actor.UserID, "Bastion", "")
	if err != nil || binding == nil || binding.StepUpProof == "" {
		t.Fatalf("解绑后空证明应能开始绑定: %v %+v", err, binding)
	}
	got, err := users.GetByID(ctx, actor.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MFAType != auth.FactorTOTP || !got.MFATotpPending || got.MFAEnabled {
		t.Fatalf("开始绑定后应预置 totp 待确认: type=%s pending=%v enabled=%v", got.MFAType, got.MFATotpPending, got.MFAEnabled)
	}
}

func TestStartTOTPBindingRequiresProofWhenActivated(t *testing.T) {
	users := &memAuthUsers{}
	actor := hashedUser(t, 206, "sysadmin", "totp", true)
	actor.MFAFactorActivated = true
	actor.MFASecretCipher = "cipher"
	actor.MFATotpPending = false
	users.add(actor)
	svc, _, _ := newTOTPAuth(t, users)
	if _, err := svc.StartTOTPBinding(context.Background(), actor.UserID, "Bastion", ""); !errors.Is(err, ErrStepUpRejected) {
		t.Fatalf("已激活动态令牌时侯空证明不能开始绑定: %v", err)
	}
}
