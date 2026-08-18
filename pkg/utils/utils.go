package utils

import (
	"fmt"
	"time"
)

// fixedWindowKey is the redis key holding the request count for one client
// within one window.
func FixedWindowKey(ip string, windowStart time.Time) string {
	return fmt.Sprintf("ratelimit:fixed:%s:%d", ip, windowStart.Unix())
}

// TokenBucketKey is the redis hash holding one client's token count and the
// timestamp its tokens were last topped up.
func TokenBucketKey(ip string) string {
	return fmt.Sprintf("ratelimit:token:%s", ip)
}
