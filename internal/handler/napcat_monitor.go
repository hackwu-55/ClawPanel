package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zhaoxinyi02/ClawPanel/internal/monitor"
)

// GetNapCatStatus returns the current NapCat connection status
func GetNapCatStatus(mon *monitor.NapCatMonitor) gin.HandlerFunc {
	return func(c *gin.Context) {
		status := mon.GetStatus()
		c.JSON(http.StatusOK, gin.H{
			"ok":     true,
			"status": status,
		})
	}
}

// GetNapCatReconnectLogs returns reconnection history
func GetNapCatReconnectLogs(mon *monitor.NapCatMonitor) gin.HandlerFunc {
	return func(c *gin.Context) {
		logs := mon.GetLogs()
		c.JSON(http.StatusOK, gin.H{
			"ok":   true,
			"logs": logs,
		})
	}
}

// NapCatReconnect manually triggers a reconnection
func NapCatReconnect(mon *monitor.NapCatMonitor) gin.HandlerFunc {
	return func(c *gin.Context) {
		err := mon.Reconnect()
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"ok":    false,
				"error": err.Error(),
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"ok":      true,
			"message": "重连请求已发送",
		})
	}
}

// NapCatMonitorConfig updates monitor settings
func NapCatMonitorConfig(mon *monitor.NapCatMonitor) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			AutoReconnect *bool `json:"autoReconnect,omitempty"`
			MaxReconnect  *int  `json:"maxReconnect,omitempty"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "参数错误"})
			return
		}
		if req.AutoReconnect != nil {
			mon.SetAutoReconnect(*req.AutoReconnect)
		}
		if req.MaxReconnect != nil {
			mon.SetMaxReconnect(*req.MaxReconnect)
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}
