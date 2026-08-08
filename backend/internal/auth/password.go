package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

// HashCost is the bcrypt cost used for new passwords.
//
// Cost is a deliberate trade: each increment doubles the work an attacker must
// do per guess, and doubles the time a legitimate login takes. 11 puts a single
// verification in the region of a tenth of a second on the hardware this runs
// on, which is unnoticeable to a person signing in and expensive in bulk.
const HashCost = 11

// ErrWeakPassword is returned when a password fails the policy.
var ErrWeakPassword = errors.New("password rejected")

// MinPasswordLength is the shortest password accepted.
//
// Length does more for a password than any composition rule, so the policy
// leans on it and asks for only a little variety on top.
const MinPasswordLength = 10

// obvious lists passwords that are guessed first, whatever their length.
var obvious = map[string]bool{
	"password":     true,
	"password123":  true,
	"letmein":      true,
	"welcome":      true,
	"welcome123":   true,
	"changeme":     true,
	"qwertyuiop":   true,
	"1234567890":   true,
	"sugarfactory": true,
	"kampongspeu":  true,
}

// CheckPasswordPolicy reports whether a password may be used for an account.
//
// The rules are stated in the error text rather than hidden behind "invalid
// password", because a person cannot satisfy a rule they are not told.
func CheckPasswordPolicy(username, password string) error {
	if len(password) < MinPasswordLength {
		return fmt.Errorf("%w: it must be at least %d characters long", ErrWeakPassword, MinPasswordLength)
	}
	// bcrypt silently ignores anything past 72 bytes, so a longer password
	// would be weaker than it looks. Say so instead.
	if len(password) > 72 {
		return fmt.Errorf("%w: it must be at most 72 characters long", ErrWeakPassword)
	}
	if obvious[strings.ToLower(password)] {
		return fmt.Errorf("%w: it is one of the first passwords anyone tries", ErrWeakPassword)
	}
	if username != "" && strings.EqualFold(password, username) {
		return fmt.Errorf("%w: it must not be the user name", ErrWeakPassword)
	}

	var letters, others bool
	for _, r := range password {
		switch {
		case unicode.IsLetter(r):
			letters = true
		default:
			others = true
		}
	}
	if !letters || !others {
		return fmt.Errorf("%w: it must mix letters with at least one digit or symbol", ErrWeakPassword)
	}
	return nil
}

// HashPassword returns a bcrypt digest of the password.
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), HashCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// VerifyPassword reports whether the password matches the stored digest.
//
// An account with no digest never matches, but still costs a comparison: the
// caller must not be able to tell "no such user" from "wrong password" by
// timing the answer.
func VerifyPassword(hash, password string) bool {
	if hash == "" {
		hash = decoyHash()
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// decoyHash is a real bcrypt digest of a value nobody knows, so that verifying
// against an account that cannot sign in costs the same as verifying against
// one that can. It is computed once, and only if it is ever needed.
var decoyHash = sync.OnceValue(func() string {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		// Falling back to a fixed value only weakens the timing defence; it
		// must not stop anyone signing in.
		secret = []byte("no entropy available for the decoy hash")
	}
	h, err := bcrypt.GenerateFromPassword(secret, HashCost)
	if err != nil {
		return ""
	}
	return string(h)
})

// NewSessionToken returns an opaque session token and the hash to store.
//
// 32 bytes from crypto/rand is well beyond guessing. The token goes to the
// browser; only the hash is written to the database, so a database copy yields
// no usable session.
func NewSessionToken() (token string, hash []byte, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generate session token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token), nil
}

// HashToken returns the stored form of a session token.
//
// SHA-256 rather than bcrypt: unlike a password, the token is 256 bits of
// randomness, so there is nothing to slow an attacker down about — and this
// runs on every authenticated request.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// GeneratePassword returns a readable random password for an administrator to
// hand over, e.g. "hp4t-9wqm-2xdv".
//
// The alphabet leaves out characters that are read back wrongly over a phone
// or off a whiteboard: 0/O, 1/l/I, and the vowels that let it spell something
// unfortunate.
func GeneratePassword() (string, error) {
	const alphabet = "abcdefghjkmnpqrstvwxyz23456789"
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}

	var b strings.Builder
	for i, v := range buf {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(alphabet[int(v)%len(alphabet)])
	}
	return b.String(), nil
}
