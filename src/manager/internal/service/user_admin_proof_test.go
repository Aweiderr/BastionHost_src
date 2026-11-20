package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"bastion.local/shared/auth"
)

func TestPreFormProofAcceptsLaterFactorOnSameTarget(t *testing.T) {
	users := &memAuthUsers{}
	actor := hashedUser(t, 81, "sysadmin", "none", false)
	users.add(actor)
	svc, _, _ := newTOTPAuth(t, users)
	ctx := context.Background()
	userSvc := &UserService{authSvc: svc}

	res, err := svc.BeginStepUp(ctx, StepUpRequest{
		ActorID: actor.UserID, Password: gatewayTestPassword,
		Action: auth.StepUpMFAAssign, TargetUserID: 0, NewFactor: "",
	})
	if err != nil || res.Proof == "" {
		t.Fatalf("创建用户前的核验应签发证明: %v", err)
	}
	if err := userSvc.commitUserAuthChange(ctx, res.Proof, actor.UserID, 9, auth.StepUpMFAAssign, auth.FactorSMS, func(pgx.Tx) error {
		t.Fatal("目标不一致时不能写入")
		return nil
	}); !errors.Is(err, ErrStepUpRejected) {
		t.Fatalf("创建证明不能用于别的用户: %v", err)
	}
	called := 0
	if err := userSvc.commitUserAuthChange(ctx, res.Proof, actor.UserID, 0, auth.StepUpMFAAssign, auth.FactorSMS, func(pgx.Tx) error {
		called++
		return nil
	}); err != nil || called != 1 {
		t.Fatalf("空因子证明应能用于随后选定的短信因子: called=%d err=%v", called, err)
	}
	if err := userSvc.commitUserAuthChange(ctx, res.Proof, actor.UserID, 0, auth.StepUpMFAAssign, auth.FactorUKey, func(pgx.Tx) error {
		return nil
	}); !errors.Is(err, ErrStepUpRejected) {
		t.Fatalf("证明不能重放: %v", err)
	}
}

func TestBoundFactorProofCannotChangeFactor(t *testing.T) {
	users := &memAuthUsers{}
	actor := hashedUser(t, 82, "sysadmin", "none", false)
	users.add(actor)
	svc, _, _ := newTOTPAuth(t, users)
	ctx := context.Background()
	userSvc := &UserService{authSvc: svc}
	res, err := svc.BeginStepUp(ctx, StepUpRequest{
		ActorID: actor.UserID, Password: gatewayTestPassword,
		Action: auth.StepUpMFAAssign, TargetUserID: 9, NewFactor: auth.FactorTOTP,
	})
	if err != nil || res.Proof == "" {
		t.Fatal(err)
	}
	if err := userSvc.commitUserAuthChange(ctx, res.Proof, actor.UserID, 9, auth.StepUpMFAAssign, auth.FactorSMS, func(pgx.Tx) error {
		t.Fatal("写明动态口令的证明不能改成短信")
		return nil
	}); !errors.Is(err, ErrStepUpRejected) {
		t.Fatalf("因子被绑死的证明不能换因子: %v", err)
	}
	if err := userSvc.commitUserAuthChange(ctx, res.Proof, actor.UserID, 9, auth.StepUpGrantPrivileged, auth.FactorUKey, func(pgx.Tx) error {
		t.Fatal("写明因子的证明不能拿去授予管理员")
		return nil
	}); !errors.Is(err, ErrStepUpRejected) {
		t.Fatalf("指定因子的证明不能改作授予管理员: %v", err)
	}
	called := 0
	if err := userSvc.commitUserAuthChange(ctx, res.Proof, actor.UserID, 9, auth.StepUpMFAAssign, auth.FactorTOTP, func(pgx.Tx) error {
		called++
		return nil
	}); err != nil || called != 1 {
		t.Fatalf("原因子仍应能提交: called=%d err=%v", called, err)
	}
}

func TestEditPreFormProofCoversFactorPhoneAndPrivilege(t *testing.T) {
	users := &memAuthUsers{}
	actor := hashedUser(t, 83, "sysadmin", "none", false)
	users.add(actor)
	svc, _, _ := newTOTPAuth(t, users)
	ctx := context.Background()
	userSvc := &UserService{authSvc: svc}

	issue := func(target int64) string {
		t.Helper()
		res, err := svc.BeginStepUp(ctx, StepUpRequest{
			ActorID: actor.UserID, Password: gatewayTestPassword,
			Action: auth.StepUpMFAAssign, TargetUserID: target, NewFactor: "",
		})
		if err != nil || res.Proof == "" {
			t.Fatalf("编辑前核验失败: %v", err)
		}
		return res.Proof
	}

	if err := userSvc.commitUserAuthChange(ctx, issue(15), actor.UserID, 15, auth.StepUpMFAAssign, auth.FactorUKey, func(pgx.Tx) error {
		return nil
	}); err != nil {
		t.Fatalf("编辑证明应允许随后把因子改成 UKey: %v", err)
	}
	if err := userSvc.commitUserAuthChange(ctx, issue(16), actor.UserID, 16, auth.StepUpMFAPhone, auth.FactorSMS, func(pgx.Tx) error {
		return nil
	}); err != nil {
		t.Fatalf("编辑证明应允许随后修改短信手机号: %v", err)
	}
	if err := userSvc.commitUserAuthChange(ctx, issue(17), actor.UserID, 17, auth.StepUpGrantPrivileged, auth.FactorUKey, func(pgx.Tx) error {
		return nil
	}); err != nil {
		t.Fatalf("编辑证明应允许随后授予管理员: %v", err)
	}

	proof := issue(18)
	if err := userSvc.commitUserAuthChange(ctx, proof, actor.UserID, 18, auth.StepUpUKeyBind, "", func(pgx.Tx) error {
		t.Fatal("用户编辑证明不能拿去绑定证书")
		return nil
	}); !errors.Is(err, ErrStepUpRejected) {
		t.Fatalf("编辑证明不能改作证书绑定: %v", err)
	}
	if err := userSvc.commitUserAuthChange(ctx, proof, actor.UserID, 19, auth.StepUpMFAAssign, auth.FactorSMS, func(pgx.Tx) error {
		t.Fatal("不能写到别的用户")
		return nil
	}); !errors.Is(err, ErrStepUpRejected) {
		t.Fatalf("编辑证明不能换目标: %v", err)
	}
	if err := userSvc.commitUserAuthChange(ctx, proof, actor.UserID, 18, auth.StepUpMFAAssign, auth.FactorSMS, func(pgx.Tx) error {
		return nil
	}); err != nil {
		t.Fatalf("未消费的编辑证明仍应用于原目标: %v", err)
	}
}
