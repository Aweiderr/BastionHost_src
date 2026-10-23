package proxy

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"golang.org/x/crypto/ssh"
)

func testSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEvaluateHostKey_Strict(t *testing.T) {
	s := testSigner(t)
	fp := ssh.FingerprintSHA256(s.PublicKey())

	if _, err := EvaluateHostKey(HostKeyPolicyStrict, "", s.PublicKey()); !errors.Is(err, ErrHostKeyUnknown) {
		t.Fatalf("empty expected: want ErrHostKeyUnknown, got %v", err)
	}
	if _, err := EvaluateHostKey(HostKeyPolicyStrict, fp, s.PublicKey()); err != nil {
		t.Fatalf("match: %v", err)
	}
	if _, err := EvaluateHostKey(HostKeyPolicyStrict, "SHA256:deadbeef", s.PublicKey()); !errors.Is(err, ErrHostKeyMismatch) {
		t.Fatalf("mismatch: want ErrHostKeyMismatch, got %v", err)
	}
}

func TestEvaluateHostKey_TOFU(t *testing.T) {
	s := testSigner(t)
	fp := ssh.FingerprintSHA256(s.PublicKey())

	check, err := EvaluateHostKey(HostKeyPolicyTOFU, "", s.PublicKey())
	if err != nil || !check.Learned {
		t.Fatalf("first see: err=%v learned=%v", err, check != nil && check.Learned)
	}
	if _, err := EvaluateHostKey(HostKeyPolicyTOFU, fp, s.PublicKey()); err != nil {
		t.Fatalf("known match: %v", err)
	}
	if _, err := EvaluateHostKey(HostKeyPolicyTOFU, "SHA256:other", s.PublicKey()); !errors.Is(err, ErrHostKeyMismatch) {
		t.Fatalf("changed: want mismatch, got %v", err)
	}
}

func TestEvaluateHostKey_Insecure(t *testing.T) {
	s := testSigner(t)
	if _, err := EvaluateHostKey(HostKeyPolicyInsecure, "SHA256:x", s.PublicKey()); err != nil {
		t.Fatal(err)
	}
}

func TestFingerprintsEqual(t *testing.T) {
	if !FingerprintsEqual("sha256:AbC", "SHA256:AbC") {
		t.Fatal("case fold algo")
	}
	if FingerprintsEqual("SHA256:a", "SHA256:b") {
		t.Fatal("should differ")
	}
}
