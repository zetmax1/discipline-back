package routes

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// SetupIPDebug registers GET /debug/ip when enabled (ENABLE_IP_DEBUG=true). It
// echoes the caller's raw forwarding headers so the hosting platform's proxy
// behaviour can be checked before choosing TRUSTED_PROXIES / TRUSTED_IP_HEADER.
func SetupIPDebug(r *gin.Engine, enabled bool) {
	if !enabled {
		return
	}
	log.Println("WARNING: ENABLE_IP_DEBUG=true, /debug/ip is exposed; turn it off after checking proxy headers")
	r.GET("/debug/ip", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{
			"xForwardedFor": nonNil(c.Request.Header.Values("X-Forwarded-For")),
			"xRealIp":       nonNil(c.Request.Header.Values("X-Real-IP")),
			"remoteAddr":    c.Request.RemoteAddr,
			"clientIp":      c.ClientIP(), // what the rate limiter keys on with the current settings
		})
	})
}

// nonNil makes an absent header encode as [] rather than null.
func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
