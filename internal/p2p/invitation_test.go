package p2p

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestInvitationLifetimeAndCopiedFormatting(t *testing.T) {
	network := testNetwork(t)
	before := time.Now()
	invite, err := network.CreateInvitation()
	if err != nil {
		t.Fatal(err)
	}
	if invite.ExpiresAt.Before(before.Add(8*time.Hour)) || invite.ExpiresAt.After(time.Now().Add(8*time.Hour)) {
		t.Fatal("invitation does not have an eight-hour lifetime")
	}
	original, err := parseInvitation(invite.Code)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := "\ufeff  " + invite.Code[:25] + "\r\n\t" + invite.Code[25:70] + "\u200b\u2060\u00a0" + invite.Code[70:] + "\n"
	parsed, err := parseInvitation(wrapped)
	if err != nil || parsed != original {
		t.Fatalf("copy formatting changed or invalidated the invitation: %v", err)
	}
	// Short-lived invites from old clients remain valid; changes do not alter the
	// encoding or require a new wire version.
	old := original
	old.Expires = time.Now().Add(5 * time.Minute)
	encoded, _ := json.Marshal(old)
	if _, err := parseInvitation("linkport1." + base64.RawURLEncoding.EncodeToString(encoded)); err != nil {
		t.Fatalf("legacy invitation rejected: %v", err)
	}
}

func TestInvitationErrorsDoNotExposeInvitationContents(t *testing.T) {
	network := testNetwork(t)
	invite, err := network.CreateInvitation()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := parseInvitation(invite.Code)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(value invitePayload) string {
		data, _ := json.Marshal(value)
		return "linkport1." + base64.RawURLEncoding.EncodeToString(data)
	}
	expired, future, wrongKey, wrongVersion := payload, payload, payload, payload
	expired.Expires = time.Now().Add(-time.Second)
	future.Expires = time.Now().Add(9 * time.Hour)
	wrongKey.Peer.ID = strings.Repeat("a", 64)
	wrongVersion.Version = 2
	for _, test := range []struct {
		name, code, message string
	}{
		{"missing-prefix", invite.Code[1:], "应以 linkport1."},
		{"invalid-encoding", invite.Code + "!", "编码不完整"},
		{"truncated-content", "linkport1." + base64.RawURLEncoding.EncodeToString([]byte(`{"version":1`)), "内容不完整"},
		{"expired", encode(expired), "已过期"},
		{"clock-skew", encode(future), "系统时间"},
		{"identity-mismatch", encode(wrongKey), "does not match"},
		{"unsupported-version", encode(wrongVersion), "版本"},
		{"oversize-formatting", strings.Repeat(" ", 24*1024) + invite.Code, "过长"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseInvitation(test.code)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected %q, got %v", test.message, err)
			}
			if strings.Contains(err.Error(), payload.Token) || strings.Contains(err.Error(), invite.Code) {
				t.Fatal("invitation credentials included in the error")
			}
		})
	}
}

func TestCopiedInvitationStillRequiresSingleUseAuthorization(t *testing.T) {
	a, b := testNetwork(t), testNetwork(t)
	invite, err := a.CreateInvitation()
	if err != nil {
		t.Fatal(err)
	}
	wrapped := invite.Code[:20] + " \r\n" + invite.Code[20:]
	if _, err := b.AddDevice(t.Context(), wrapped); err != nil {
		t.Fatalf("formatted invitation could not pair: %v", err)
	}
	if _, err := b.AddDevice(t.Context(), wrapped); err == nil {
		t.Fatal("format normalization permitted invitation reuse")
	}
}
