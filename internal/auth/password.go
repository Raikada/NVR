package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters for LocalUser password hashing. Mirrors
// management/internal/auth/password.go exactly so the recorder-local
// hash format is byte-equivalent to the MS's. ~50ms hash time on a
// typical server CPU.
const (
	argonMemory      = 64 * 1024 // 64 MiB
	argonIterations  = 3
	argonParallelism = 2
	argonKeyLen      = 32
	argonSaltLen     = 16
)

// HashPassword produces a PHC-format argon2id hash. Output embeds the
// parameters so the recorder can change them later without
// re-hashing existing passwords on read.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("empty password")
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("rand salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLen)
	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonIterations, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash))
	return encoded, nil
}

// VerifyPassword checks a candidate password against a stored
// argon2id hash. Constant-time comparison.
func VerifyPassword(stored, candidate string) (bool, error) {
	parts := strings.Split(stored, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("not an argon2id hash")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("parse version: %w", err)
	}
	if version != argon2.Version {
		return false, fmt.Errorf("unsupported argon2 version %d", version)
	}
	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false, fmt.Errorf("parse params: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("decode salt: %w", err)
	}
	wantHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("decode hash: %w", err)
	}
	gotHash := argon2.IDKey([]byte(candidate), salt, iterations, memory, parallelism, uint32(len(wantHash)))
	if subtle.ConstantTimeCompare(gotHash, wantHash) == 1 {
		return true, nil
	}
	return false, nil
}
