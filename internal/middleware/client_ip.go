package middleware

import (
	"github.com/gin-gonic/gin"
)

// ConfigureClientIP controls where c.ClientIP() reads the client address from.
// trustedProxies lists proxy IPs/CIDRs whose X-Forwarded-For is honoured; empty
// trusts none, so the socket peer address is used. ipHeader, if set, names a
// header the hosting platform always overwrites with the real client IP.
func ConfigureClientIP(r *gin.Engine, trustedProxies []string, ipHeader string) error {
	if len(trustedProxies) == 0 {
		trustedProxies = nil
	}
	if err := r.SetTrustedProxies(trustedProxies); err != nil {
		return err
	}
	r.TrustedPlatform = ipHeader
	return nil
}
