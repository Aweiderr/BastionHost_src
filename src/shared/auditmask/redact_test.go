package auditmask

import (
	"strings"
	"testing"
)

func TestRedactPasswordIDPhoneBank(t *testing.T) {
	rules := BuiltinRules()
	raw := strings.Join([]string{
		"password=s3cret-value",
		"ALTER USER app IDENTIFIED BY s3cret-value",
		"id 110101199003078515",
		"mobile 13900001111",
		"card 4111111111111111",
	}, "\n")
	got := Apply(raw, rules)
	for _, secret := range []string{"s3cret-value", "110101199003078515", "13900001111", "4111111111111111"} {
		if strings.Contains(got, secret) {
			t.Fatalf("展示文本仍包含 %s\n%s", secret, got)
		}
	}
	if !strings.Contains(got, "password=******") || !strings.Contains(got, "IDENTIFIED BY ******") {
		t.Fatalf("口令字段没有按字段脱敏: %s", got)
	}
	if !strings.Contains(got, "110101********8515") {
		t.Fatalf("身份证号掩码不符: %s", got)
	}
	if !strings.Contains(got, "139****1111") {
		t.Fatalf("手机号掩码不符: %s", got)
	}
	if !strings.Contains(got, "411111******1111") {
		t.Fatalf("银行卡掩码不符: %s", got)
	}
}

func TestRedactDoesNotBreakSearch(t *testing.T) {
	rules := BuiltinRules()
	raw := "pay card 4111111111111111 and phone 13900001111"
	if !KeywordHits(raw, "4111111111111111") {
		t.Fatal("原文检索必须命中银行卡")
	}
	if !KeywordHits(raw, "13900001111") {
		t.Fatal("原文检索必须命中手机号")
	}
	shown := Apply(raw, rules)
	if KeywordHits(shown, "4111111111111111") || KeywordHits(shown, "13900001111") {
		t.Fatalf("展示副本不应再包含完整号码: %s", shown)
	}
	// 检索用的是原文，脱敏只影响展示。同一条规则跑两次结果不变。
	if Apply(shown, rules) != shown {
		t.Fatal("脱敏应当幂等")
	}
}

func TestBankCardIgnoresNonLuhn(t *testing.T) {
	raw := "ticket 1234567890123456"
	got := Apply(raw, []Rule{{Kind: KindField, FieldType: FieldBankCard, Enabled: true}})
	if got != raw {
		t.Fatalf("未通过校验的数字不应整段打码: %s", got)
	}
}

func TestGraphicalJpegIsNotRedacted(t *testing.T) {
	jpeg := "abc13900001111xyz"
	plain := "BSG1 1\n" + `{"type":"frame","jpeg":"` + jpeg + `","note":"password=s3cret-value"}` + "\n"
	got := string(RedactPlayback([]byte(plain), BuiltinRules()))
	if !strings.Contains(got, jpeg) {
		t.Fatalf("画面 base64 被改写了: %s", got)
	}
	if strings.Contains(got, "s3cret-value") {
		t.Fatalf("画面以外的文本应当脱敏: %s", got)
	}
}

func TestMergeRulesTenantCanDisableGlobal(t *testing.T) {
	global := []Rule{{Name: "内置-手机号", Kind: KindField, FieldType: FieldPhone, Enabled: true}}
	tenant := []Rule{{Name: "内置-手机号", Kind: KindField, FieldType: FieldPhone, Enabled: false}}
	if len(MergeRules(global, tenant)) != 0 {
		t.Fatal("租户停用同名规则后，全局规则不应再生效")
	}
	tenant[0].Enabled = true
	tenant[0].FieldType = FieldIDCard
	merged := MergeRules(global, tenant)
	if len(merged) != 1 || merged[0].FieldType != FieldIDCard {
		t.Fatalf("租户规则应覆盖全局: %+v", merged)
	}
}

func TestKeywordAndRegex(t *testing.T) {
	rules := []Rule{
		{Kind: KindKeyword, Pattern: "PROJECT-TOKEN", Replacement: "******", Enabled: true},
		{Kind: KindRegex, Pattern: `ticket-([0-9]{4})`, Replacement: `ticket-****`, Enabled: true},
	}
	got := Apply("see PROJECT-TOKEN ticket-8899", rules)
	if strings.Contains(got, "PROJECT-TOKEN") || strings.Contains(got, "8899") {
		t.Fatalf("关键字或正则没有生效: %s", got)
	}
}
