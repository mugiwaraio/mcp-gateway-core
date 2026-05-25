package httpaccess

import (
	"net/http"
	"testing"
)

func newReq(remoteAddr, xff string) *http.Request {
	r, _ := http.NewRequest("POST", "/mcp", nil)
	r.RemoteAddr = remoteAddr
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

func TestIPExtractor_NoTrustedProxies_ReturnsRemoteAddr(t *testing.T) {
	e, err := NewIPExtractor(nil)
	if err != nil {
		t.Fatal(err)
	}
	got := e.ClientIP(newReq("1.2.3.4:5678", "9.9.9.9"))
	if got != "1.2.3.4" {
		t.Fatalf("got %q, want 1.2.3.4", got)
	}
}

func TestIPExtractor_RemoteAddrNotTrusted_IgnoresXFF(t *testing.T) {
	e, _ := NewIPExtractor([]string{"10.0.0.0/8"})
	got := e.ClientIP(newReq("1.2.3.4:5678", "9.9.9.9, 8.8.8.8"))
	if got != "1.2.3.4" {
		t.Fatalf("got %q, want 1.2.3.4", got)
	}
}

func TestIPExtractor_RemoteAddrTrusted_SingleXFF(t *testing.T) {
	e, _ := NewIPExtractor([]string{"10.0.0.0/8"})
	got := e.ClientIP(newReq("10.0.0.5:5678", "1.2.3.4"))
	if got != "1.2.3.4" {
		t.Fatalf("got %q, want 1.2.3.4", got)
	}
}

func TestIPExtractor_ChainedXFF_RightmostUntrusted(t *testing.T) {
	e, _ := NewIPExtractor([]string{"10.0.0.0/8", "192.168.0.0/16"})
	got := e.ClientIP(newReq("10.0.0.5:1", "1.2.3.4, 192.168.1.10, 10.0.0.5"))
	if got != "1.2.3.4" {
		t.Fatalf("got %q, want 1.2.3.4", got)
	}
}

func TestIPExtractor_AllXFFTrusted_ReturnsLeftmost(t *testing.T) {
	e, _ := NewIPExtractor([]string{"10.0.0.0/8"})
	got := e.ClientIP(newReq("10.0.0.5:1", "10.1.1.1, 10.2.2.2"))
	if got != "10.1.1.1" {
		t.Fatalf("got %q, want 10.1.1.1", got)
	}
}

func TestIPExtractor_NoXFF_TrustedRemote_FallsBack(t *testing.T) {
	e, _ := NewIPExtractor([]string{"10.0.0.0/8"})
	got := e.ClientIP(newReq("10.0.0.5:1", ""))
	if got != "10.0.0.5" {
		t.Fatalf("got %q, want 10.0.0.5", got)
	}
}

func TestIPExtractor_InvalidCIDR_Error(t *testing.T) {
	_, err := NewIPExtractor([]string{"not-a-cidr"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestIPExtractor_IPv6_Bracketed(t *testing.T) {
	e, _ := NewIPExtractor(nil)
	got := e.ClientIP(newReq("[::1]:8080", ""))
	if got != "::1" {
		t.Fatalf("got %q, want ::1", got)
	}
}

func TestIPExtractor_RemoteAddrWithoutPort(t *testing.T) {
	e, _ := NewIPExtractor(nil)
	got := e.ClientIP(newReq("1.2.3.4", ""))
	if got != "1.2.3.4" {
		t.Fatalf("got %q, want 1.2.3.4", got)
	}
}

func TestIPExtractor_XFFWithGarbage_SkipsIt(t *testing.T) {
	// 不可解析的 token 视为 untrusted，循环直接返回它
	e, _ := NewIPExtractor([]string{"10.0.0.0/8"})
	got := e.ClientIP(newReq("10.0.0.5:1", "garbage"))
	if got != "garbage" {
		t.Fatalf("got %q, want garbage", got)
	}
}

func TestHasUnrestrictedCIDR_AllZero_IPv4(t *testing.T) {
	if !HasUnrestrictedCIDR([]string{"0.0.0.0/0"}) {
		t.Fatal("expected true for 0.0.0.0/0")
	}
}

func TestHasUnrestrictedCIDR_AllZero_IPv6(t *testing.T) {
	if !HasUnrestrictedCIDR([]string{"::/0"}) {
		t.Fatal("expected true for ::/0")
	}
}

func TestHasUnrestrictedCIDR_Restricted(t *testing.T) {
	if HasUnrestrictedCIDR([]string{"10.0.0.0/8", "192.168.0.0/16"}) {
		t.Fatal("expected false for restricted CIDRs")
	}
}

func TestHasUnrestrictedCIDR_Empty(t *testing.T) {
	if HasUnrestrictedCIDR(nil) {
		t.Fatal("expected false for empty list")
	}
}
