package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("hunter2")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("expected argon2id prefix, got: %s", hash)
	}
	ok, err := VerifyPassword(hash, "hunter2")
	if err != nil || !ok {
		t.Fatalf("verify good password: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword(hash, "wrong")
	if err != nil || ok {
		t.Fatalf("verify bad password: ok=%v err=%v", ok, err)
	}
}

func TestHashEmptyPasswordRejected(t *testing.T) {
	_, err := HashPassword("")
	if err == nil {
		t.Fatal("expected error for empty password")
	}
}

func TestVerifyMalformedHash(t *testing.T) {
	cases := []string{
		"",
		"not-a-hash",
		"$bcrypt$v=19$x$y$z",
		"$argon2id$v=999$m=1,t=1,p=1$" + "AAAA" + "$" + "BBBB",
	}
	for _, c := range cases {
		_, err := VerifyPassword(c, "anything")
		if err == nil {
			t.Errorf("expected error for hash %q", c)
		}
	}
}

func TestHashesAreUniquePerCall(t *testing.T) {
	h1, _ := HashPassword("same")
	h2, _ := HashPassword("same")
	if h1 == h2 {
		t.Fatal("hashes should differ due to per-call salt")
	}
}
