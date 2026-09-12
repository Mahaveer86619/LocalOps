// Package slack verifies inbound Slack requests (slash commands). It is
// deliberately separate from internal/notify, which only ever sends
// outbound webhook messages - this package only ever authenticates
// requests Slack sends to us.
package slack

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

// maxRequestAge rejects a slash-command request whose timestamp is
// further than this from now, in either direction - Slack's own
// recommendation, to block replay of a captured request.
const maxRequestAge = 5 * time.Minute

// VerifySignature checks an inbound Slack request against Slack's v0
// signing scheme: https://api.slack.com/authentication/verifying-requests-from-slack
//
// signingSecret is the Slack app's Signing Secret (Basic Information page).
// timestamp and signature are the X-Slack-Request-Timestamp and
// X-Slack-Signature headers, verbatim. body is the raw, unparsed request
// body (verification covers the exact bytes Slack sent, so this must run
// before the body is parsed as a form).
func VerifySignature(signingSecret, timestamp, signature string, body []byte) bool {
	if signingSecret == "" || timestamp == "" || signature == "" {
		return false
	}

	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	age := time.Since(time.Unix(ts, 0))
	if age < 0 {
		age = -age
	}
	if age > maxRequestAge {
		return false
	}

	base := "v0:" + timestamp + ":" + string(body)
	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte(base))
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(signature))
}
