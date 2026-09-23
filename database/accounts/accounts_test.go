package accounts

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// 新哈希必须是 bcrypt 格式，且只能用 bcrypt 校验通过
func TestHashPasswdIsBcrypt(t *testing.T) {
	h, err := hashPasswd("s3cret-p@ssw0rd")
	if err != nil {
		t.Fatalf("hashPasswd returned error: %v", err)
	}
	if !strings.HasPrefix(h, "$2") {
		t.Fatalf("expected bcrypt hash, got: %q", h)
	}
	if len(h) != 60 {
		t.Fatalf("expected 60-char bcrypt hash, got %d chars", len(h))
	}
	if err := bcrypt.CompareHashAndPassword([]byte(h), []byte("s3cret-p@ssw0rd")); err != nil {
		t.Fatalf("bcrypt cannot verify its own hash: %v", err)
	}
}

// bcrypt 密码：正确通过、错误拒绝、无需升级
func TestVerifyPasswdBcrypt(t *testing.T) {
	h, err := hashPasswd("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	ok, upgraded := verifyPasswd("correct horse", h)
	if !ok || upgraded != "" {
		t.Fatalf("bcrypt verify failed: ok=%v upgraded=%q", ok, upgraded)
	}
	ok, _ = verifyPasswd("wrong password", h)
	if ok {
		t.Fatal("bcrypt accepted wrong password")
	}
}

// 旧版 SHA256 哈希：正确通过并返回升级后的 bcrypt 哈希；升级哈希必须能用 bcrypt 校验
func TestVerifyPasswdLegacyUpgrade(t *testing.T) {
	legacy := legacyHashPasswd("old-password")
	if strings.HasPrefix(legacy, "$2") {
		t.Fatal("legacy hash must not look like bcrypt")
	}
	ok, upgraded := verifyPasswd("old-password", legacy)
	if !ok {
		t.Fatal("legacy hash rejected correct password")
	}
	if upgraded == "" {
		t.Fatal("legacy verify did not produce an upgrade hash")
	}
	if !strings.HasPrefix(upgraded, "$2") {
		t.Fatalf("upgrade hash is not bcrypt: %q", upgraded)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(upgraded), []byte("old-password")); err != nil {
		t.Fatalf("upgrade hash does not verify: %v", err)
	}
	// 升级后的哈希再次验证：走 bcrypt 路径，不应再触发升级
	ok, upgraded2 := verifyPasswd("old-password", upgraded)
	if !ok || upgraded2 != "" {
		t.Fatalf("re-verify of upgraded hash wrong: ok=%v upgraded2=%q", ok, upgraded2)
	}
	ok, _ = verifyPasswd("wrong password", legacy)
	if ok {
		t.Fatal("legacy hash accepted wrong password")
	}
}

// 旧版哈希与新哈希对同一密码必须不同（证明盐不再是固定常量）
func TestNewHashDiffersFromLegacy(t *testing.T) {
	newHash, err := hashPasswd("same-password")
	if err != nil {
		t.Fatal(err)
	}
	if newHash == legacyHashPasswd("same-password") {
		t.Fatal("new hash equals legacy hash: constant salt still in write path")
	}
}

// bcrypt 两次哈希同一密码应不同（随机盐）
func TestHashPasswdRandomSalt(t *testing.T) {
	h1, _ := hashPasswd("repeat-me")
	h2, _ := hashPasswd("repeat-me")
	if h1 == h2 {
		t.Fatal("two bcrypt hashes of the same password are identical: no random salt")
	}
}

// 超长密码应返回错误而非静默截断
func TestHashPasswdTooLong(t *testing.T) {
	long := strings.Repeat("x", 73)
	if _, err := hashPasswd(long); err == nil {
		t.Fatal("expected error for >72 byte password, got nil")
	}
}
