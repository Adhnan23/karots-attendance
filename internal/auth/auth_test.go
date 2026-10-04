package auth

import (
	"testing"
	"time"
)

func TestAuth(t *testing.T) {
	h := HashPassword("pass")
	if !CheckPassword(h, "pass") || CheckPassword(h, "nope") || CheckPassword("garbage", "pass") {
		t.Fatal("password check")
	}

	l := NewLimiter(3, time.Minute)
	now := time.Now()
	for range 3 {
		if l.Blocked("ip", now) {
			t.Fatal("blocked too early")
		}
		l.Fail("ip", now)
	}
	if !l.Blocked("ip", now) || l.Blocked("other", now) {
		t.Fatal("should block ip only")
	}
	if l.Blocked("ip", now.Add(2*time.Minute)) {
		t.Fatal("should unblock after window")
	}

	list := "203.0.113.7, 10.0.0.0/24\n2001:db8::1"
	for ip, want := range map[string]bool{
		"203.0.113.7": true, "10.0.0.55": true, "10.0.1.1": false, "2001:db8::1": true,
		"::ffff:10.0.0.9": true, "8.8.8.8": false, "junk": false,
	} {
		if IPAllowed(ip, list) != want {
			t.Errorf("IPAllowed(%s) != %v", ip, want)
		}
	}
	if ValidIPList(list) != "" || ValidIPList("1.2.3.4 shop") != "shop" {
		t.Fatal("ValidIPList")
	}
}
