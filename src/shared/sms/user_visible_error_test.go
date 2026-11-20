package sms

import (
	"errors"
	"testing"
)

func TestUserVisibleSendErrorMapsTencentLimits(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"腾讯云短信发送失败（LimitExceeded.PhoneNumberThirtySecondLimit，RequestId=abc）", "该手机号发送过于频繁，请 30 秒后再试"},
		{"腾讯云短信接口拒绝请求（LimitExceeded.PhoneNumberOneHourLimit）", "该手机号一小时内发送次数已达上限，请稍后再试"},
		{"LimitExceeded.PhoneNumberDailyLimit", "该手机号今日发送次数已达上限，请明日再试"},
		{"腾讯云短信发送失败（FailedOperation.NotEnterpriseCertification）", "短信发送失败，请稍后重试"},
		{"network down", "短信发送失败，请稍后重试"},
	}
	for _, tc := range cases {
		if got := UserVisibleSendError(errors.New(tc.in)); got != tc.want {
			t.Fatalf("in=%q got=%q want=%q", tc.in, got, tc.want)
		}
	}
	if UserVisibleSendError(nil) != "" {
		t.Fatal("nil 应返回空说明")
	}
}
