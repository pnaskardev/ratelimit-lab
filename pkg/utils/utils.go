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
