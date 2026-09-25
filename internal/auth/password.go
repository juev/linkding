package auth

import (
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/crypto/scrypt"
)

var ErrUnsupportedPasswordHash = errors.New("unsupported Django password hash")
var ErrInvalidPasswordHash = errors.New("invalid Django password hash")

// VerifyPassword checks the formats enabled by linkding v1.47.0. An unknown
// format is an error so migration can reject it before switching servers.
func VerifyPassword(password, encoded string) (bool, error) {
	if strings.HasPrefix(encoded, "!") { // Django's unusable password marker.
		return false, nil
	}
	parts := strings.Split(encoded, "$")
	if len(parts) == 0 {
		return false, ErrInvalidPasswordHash
	}
	switch parts[0] {
	case "pbkdf2_sha256", "pbkdf2_sha1":
		if len(parts) != 4 {
			return false, ErrInvalidPasswordHash
		}
		iterations, err := boundedInt(parts[1], 1, 20_000_000)
		if err != nil {
			return false, ErrInvalidPasswordHash
		}
		want, err := base64.StdEncoding.DecodeString(parts[3])
		if err != nil || len(want) == 0 || len(want) > 64 {
			return false, ErrInvalidPasswordHash
		}
		var digest func() hash.Hash = sha256.New
		if parts[0] == "pbkdf2_sha1" {
			digest = sha1.New
		}
		got := pbkdf2.Key([]byte(password), []byte(parts[2]), iterations, len(want), digest)
		return subtle.ConstantTimeCompare(got, want) == 1, nil
	case "scrypt":
		if len(parts) != 6 {
			return false, ErrInvalidPasswordHash
		}
		n, errN := boundedInt(parts[1], 2, 1<<20)
		r, errR := boundedInt(parts[3], 1, 32)
		p, errP := boundedInt(parts[4], 1, 32)
		want, errHash := base64.StdEncoding.DecodeString(parts[5])
		if errN != nil || errR != nil || errP != nil || errHash != nil || len(want) != 64 || n&(n-1) != 0 || uint64(n)*uint64(r)*uint64(p) > 1<<25 {
			return false, ErrInvalidPasswordHash
		}
		got, err := scrypt.Key([]byte(password), []byte(parts[2]), n, r, p, len(want))
		if err != nil {
			return false, fmt.Errorf("verify scrypt password: %w", err)
		}
		return subtle.ConstantTimeCompare(got, want) == 1, nil
	case "bcrypt_sha256":
		if len(parts) != 5 || parts[1] != "" || len(parts[2]) != 2 {
			return false, ErrInvalidPasswordHash
		}
		digest := sha256.Sum256([]byte(password))
		prehash := []byte(hex.EncodeToString(digest[:]))
		err := bcrypt.CompareHashAndPassword([]byte(strings.TrimPrefix(encoded, "bcrypt_sha256$")), prehash)
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("verify bcrypt password: %w", ErrInvalidPasswordHash)
		}
		return true, nil
	case "argon2":
		return verifyArgon2(password, parts)
	default:
		return false, fmt.Errorf("%w: %q", ErrUnsupportedPasswordHash, parts[0])
	}
}

func verifyArgon2(password string, parts []string) (bool, error) {
	// Django prepends "argon2" to the standard PHC string.
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, ErrUnsupportedPasswordHash
	}
	params := strings.Split(parts[3], ",")
	if len(params) != 3 || !strings.HasPrefix(params[0], "m=") || !strings.HasPrefix(params[1], "t=") || !strings.HasPrefix(params[2], "p=") {
		return false, ErrInvalidPasswordHash
	}
	m, errM := boundedInt(strings.TrimPrefix(params[0], "m="), 8, 1<<20)
	t, errT := boundedInt(strings.TrimPrefix(params[1], "t="), 1, 20)
	p, errP := boundedInt(strings.TrimPrefix(params[2], "p="), 1, 32)
	salt, errSalt := base64.RawStdEncoding.DecodeString(parts[4])
	want, errHash := base64.RawStdEncoding.DecodeString(parts[5])
	if errM != nil || errT != nil || errP != nil || errSalt != nil || errHash != nil || len(salt) == 0 || len(want) == 0 || len(want) > 64 || m < 8*p || uint64(m)*uint64(t) > 1<<22 {
		return false, ErrInvalidPasswordHash
	}
	got := argon2.IDKey([]byte(password), salt, uint32(t), uint32(m), uint8(p), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func boundedInt(value string, min, max int) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < min || n > max {
		return 0, ErrInvalidPasswordHash
	}
	return n, nil
}

// HashPassword creates the default Django v1.47.0 PBKDF2-SHA256 format.
func HashPassword(password string) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const iterations = 1_200_000
	var salt [22]byte
	var random [32]byte
	for i := 0; i < len(salt); {
		if _, err := rand.Read(random[:]); err != nil {
			return "", fmt.Errorf("generate password salt: %w", err)
		}
		for _, value := range random {
			if value < 248 { // 248 is the largest multiple of 62 below 256.
				salt[i] = alphabet[int(value)%len(alphabet)]
				i++
				if i == len(salt) {
					break
				}
			}
		}
	}
	digest := pbkdf2.Key([]byte(password), salt[:], iterations, sha256.Size, sha256.New)
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s", iterations, salt[:], base64.StdEncoding.EncodeToString(digest)), nil
}
