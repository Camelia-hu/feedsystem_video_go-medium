package pay

import (
	"testing"
	"time"
)

func TestSignAndParseCallback(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body, sig, err := buildCallback("secret", CallbackBody{
		OrderNo:        "P1",
		ChannelTradeNo: "M1",
		AmountFen:      700,
		Paid:           true,
		Timestamp:      now.Unix(),
		Nonce:          "abc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !verifyBody("secret", sig, body) {
		t.Fatal("signature should match")
	}
	if verifyBody("other", sig, body) {
		t.Fatal("other secret should fail")
	}
	cb, err := parseCallback(body, now)
	if err != nil {
		t.Fatal(err)
	}
	if cb.AmountFen != 700 || cb.OrderNo != "P1" {
		t.Fatalf("parsed %+v", cb)
	}
	if _, err := parseCallback(body, now.Add(10*time.Minute)); err == nil {
		t.Fatal("expired timestamp should fail")
	}
}
