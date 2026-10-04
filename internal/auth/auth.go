// Package auth holds the security primitives: password and token hashing,
// a login-failure limiter and the IP allowlist check.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const iterations = 600_000 // OWASP 2023 recommendation for PBKDF2-SHA256

// HashPassword returns "salt:key" hex for storing.
func HashPassword(pw string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	k, _ := pbkdf2.Key(sha256.New, pw, salt, iterations, 32)
	return hex.EncodeToString(salt) + ":" + hex.EncodeToString(k)
}

// CheckPassword reports whether pw matches a HashPassword result.
func CheckPassword(stored, pw string) bool {
	s, k, ok := strings.Cut(stored, ":")
	salt, _ := hex.DecodeString(s)
	want, _ := hex.DecodeString(k)
	got, _ := pbkdf2.Key(sha256.New, pw, salt, iterations, 32)
	return ok && subtle.ConstantTimeCompare(got, want) == 1
}

// DummyHash is checked for unknown phone numbers so response time doesn't reveal which exist.
var DummyHash = HashPassword(rand.Text())

// NewToken returns a random token for a cookie.
func NewToken() string { return rand.Text() }

// HashToken is what gets stored, so a leaked database can't be used to log in.
func HashToken(t string) string {
	s := sha256.Sum256([]byte(t))
	return hex.EncodeToString(s[:])
}

// Limiter counts failed logins per key (IP). After Max failures inside Window the key is blocked.
// ponytail: in memory, forgets on restart; fine for a single binary.
type Limiter struct {
	Max    int
	Window time.Duration
	mu     sync.Mutex
	fails  map[string][]time.Time
}

func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{Max: max, Window: window, fails: map[string][]time.Time{}}
}

func (l *Limiter) Blocked(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	var recent []time.Time
	for _, t := range l.fails[key] {
		if now.Sub(t) < l.Window {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(l.fails, key)
	} else {
		l.fails[key] = recent
	}
	return len(recent) >= l.Max
}

func (l *Limiter) Fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.fails) > 10_000 { // bound memory under a spray of IPs
		clear(l.fails)
	}
	l.fails[key] = append(l.fails[key], now)
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}

// IPAllowed reports whether ip matches any address or CIDR in list (comma/space/newline separated).
func IPAllowed(ip, list string) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, f := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t' }) {
		if p, err := netip.ParsePrefix(f); err == nil && p.Contains(addr) {
			return true
		}
		if x, err := netip.ParseAddr(f); err == nil && x.Unmap() == addr {
			return true
		}
	}
	return false
}

// ValidIPList returns the first entry that isn't an IP or CIDR, or "".
func ValidIPList(list string) string {
	for _, f := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t' }) {
		if _, err := netip.ParsePrefix(f); err != nil {
			if _, err := netip.ParseAddr(f); err != nil {
				return f
			}
		}
	}
	return ""
}
