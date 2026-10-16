package pay

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

const defaultCallbackSecret = "dev-pay-secret"
const callbackSkew = 5 * time.Minute

func callbackSecret() string {
	if v := os.Getenv("PAY_CALLBACK_SECRET"); v != "" {
		return v
	}
	return defaultCallbackSecret
}

func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func verifyBody(secret, signature string, body []byte) bool {
	if signature == "" {
		return false
	}
	got, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

func parseCallback(body []byte, now time.Time) (CallbackBody, error) {
	var cb CallbackBody
	if err := json.Unmarshal(body, &cb); err != nil {
		return cb, errors.New("invalid callback body")
	}
	if cb.OrderNo == "" || cb.ChannelTradeNo == "" || cb.AmountFen <= 0 || cb.Nonce == "" || cb.Timestamp == 0 {
		return cb, errors.New("callback fields are required")
	}
	ts := time.Unix(cb.Timestamp, 0)
	if ts.Before(now.Add(-callbackSkew)) || ts.After(now.Add(callbackSkew)) {
		return cb, errors.New("callback timestamp expired")
	}
	return cb, nil
}

func buildCallback(secret string, cb CallbackBody) (body []byte, signature string, err error) {
	body, err = json.Marshal(cb)
	if err != nil {
		return nil, "", err
	}
	return body, signBody(secret, body), nil
}

func newToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func knownChannel(channel string) bool {
	switch channel {
	case ChannelMock, ChannelAlipay, ChannelWechat, ChannelCard:
		return true
	default:
		return false
	}
}

func gatewayPublicURL() string {
	if v := strings.TrimSpace(os.Getenv("PAY_GATEWAY_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://127.0.0.1:8090"
}

func gatewayInternalURL() string {
	if v := strings.TrimSpace(os.Getenv("PAY_GATEWAY_INTERNAL_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return gatewayPublicURL()
}

func notifyBase() string {
	if v := strings.TrimSpace(os.Getenv("PAY_NOTIFY_BASE")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://127.0.0.1:8080"
}

func returnBase() string {
	if v := strings.TrimSpace(os.Getenv("PAY_RETURN_BASE")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://127.0.0.1:5173"
}

func signValues(secret string, v url.Values) string {
	keys := make([]string, 0, len(v))
	for k := range v {
		if k == "sign" || v.Get(k) == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(v.Get(k))
	}
	return signBody(secret, []byte(b.String()))
}

func verifyValues(secret string, v url.Values) bool {
	return hmac.Equal([]byte(signValues(secret, v)), []byte(v.Get("sign")))
}

func newOrderNo(now time.Time) (string, error) {
	token, err := newToken(4)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("P%s%s", now.UTC().Format("20060102150405"), token), nil
}
