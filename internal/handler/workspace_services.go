package handler

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	maigePort       = "8765"
	chromeRemotePort = "9223"
)

type serviceStatus struct {
	Name    string `json:"name"`
	Port    string `json:"port"`
	Running bool   `json:"running"`
	PID     int    `json:"pid,omitempty"`
}

func isPortListening(port string) bool {
	for _, host := range []string{"127.0.0.1", "localhost", "0.0.0.0", "::1"} {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 300e6)
		if err == nil {
			conn.Close()
			return true
		}
	}
	return false
}

func getServicePID(port string) int {
	portHex := fmt.Sprintf("%04X", portToHex(port))
	data, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		return 0
	}
	var inode string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		localAddr := fields[1]
		if !strings.HasSuffix(localAddr, ":"+portHex) {
			continue
		}
		// 匹配任意本地 IP (00000000 = 0.0.0.0, 0100007F = 127.0.0.1 等)
		inode = fields[9]
		break
	}
	if inode == "" {
		return 0
	}
	entries, _ := os.ReadDir("/proc")
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		fdDir := fmt.Sprintf("/proc/%d/fd", pid)
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err == nil && strings.Contains(link, "socket:["+inode+"]") {
				return pid
			}
		}
	}
	return 0
}

func portToHex(port string) int {
	i, _ := strconv.Atoi(port)
	return i
}

// GetServicesStatus 返回 maige ws 和 chrome remote 的状态
func GetServicesStatus() gin.HandlerFunc {
	return func(c *gin.Context) {
		services := []serviceStatus{
			{Name: "maige-ws", Port: maigePort, Running: isPortListening(maigePort)},
			{Name: "chrome-remote", Port: chromeRemotePort, Running: isPortListening(chromeRemotePort)},
		}
		for i := range services {
			if services[i].Running {
				services[i].PID = getServicePID(services[i].Port)
			}
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "services": services})
	}
}

// StartService 启动指定服务
func StartService() gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Name string `json:"name" binding:"required"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
			return
		}

		port := maigePort
		if body.Name == "chrome-remote" {
			port = chromeRemotePort
		}

		if isPortListening(port) {
			c.JSON(http.StatusOK, gin.H{"ok": true, "message": "服务已在运行"})
			return
		}

		var startCmd string
		switch body.Name {
		case "maige-ws":
			startCmd = fmt.Sprintf(`nohup node /home/node/.openclaw/workspace/skills/maige/server.js >/tmp/maige-ws.out 2>/tmp/maige-ws.err &`)
		case "chrome-remote":
			startCmd = fmt.Sprintf(`nohup node /home/node/.openclaw/extensions/openclaw-chrome-remote/index.js >/tmp/chrome-remote.log 2>&1 &`)
		default:
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "未知服务: " + body.Name})
			return
		}

		if err := exec.Command("sh", "-c", startCmd).Run(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "启动失败: " + err.Error()})
			return
		}

		// 等待服务就绪
		port := maigePort
		if body.Name == "chrome-remote" {
			port = chromeRemotePort
		}
		for i := 0; i < 10; i++ {
			if isPortListening(port) {
				c.JSON(http.StatusOK, gin.H{"ok": true, "message": "服务已启动"})
				return
			}
			time.Sleep(time.Second)
		}

		c.JSON(http.StatusOK, gin.H{"ok": true, "message": "服务启动命令已执行，请稍后检查状态"})
	}
}

// StopService 停止指定服务
func StopService() gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Name string `json:"name" binding:"required"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
			return
		}

		port := maigePort
		if body.Name == "chrome-remote" {
			port = chromeRemotePort
		}

		if !isPortListening(port) {
			c.JSON(http.StatusOK, gin.H{"ok": true, "message": "服务未运行"})
			return
		}

		// 用 getServicePID 查找并 kill
		pid := getServicePID(port)
		if pid > 0 {
			kill := exec.Command("kill", fmt.Sprintf("%d", pid))
			kill.Run()
		}

		// 等待端口释放
		for i := 0; i < 5; i++ {
			if !isPortListening(port) {
				c.JSON(http.StatusOK, gin.H{"ok": true, "message": "服务已停止"})
				return
			}
			time.Sleep(time.Second)
		}

		c.JSON(http.StatusOK, gin.H{"ok": true, "message": "停止命令已执行，请稍后检查状态"})
	}
}
