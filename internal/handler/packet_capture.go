package handler

import (
	"context"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/chobits02/provena/internal/config"
	"github.com/chobits02/provena/internal/database"
	"github.com/chobits02/provena/internal/packetcapture"
	"github.com/chobits02/provena/internal/security"

	"github.com/gin-gonic/gin"
)

type PacketCaptureHandler struct {
	db      *database.DB
	service *packetcapture.Service
	persist func(config.PacketCaptureConfig) error
}

func NewPacketCaptureHandler(db *database.DB, service *packetcapture.Service, persist ...func(config.PacketCaptureConfig) error) *PacketCaptureHandler {
	var save func(config.PacketCaptureConfig) error
	if len(persist) > 0 {
		save = persist[0]
	}
	return &PacketCaptureHandler{db: db, service: service, persist: save}
}

func (h *PacketCaptureHandler) Config(c *gin.Context) {
	if h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "抓包服务未初始化"})
		return
	}
	cfg := h.service.Config()
	h.bindOwner(c)
	c.JSON(http.StatusOK, gin.H{"config": cfg, "running": h.service.Running(), "address": h.service.Address(), "ca_path": h.service.CA().CertPath()})
}

func (h *PacketCaptureHandler) Status(c *gin.Context) { h.Config(c) }

func (h *PacketCaptureHandler) Start(c *gin.Context) {
	if h.service == nil {
		c.JSON(503, gin.H{"error": "抓包服务未初始化"})
		return
	}
	h.bindOwner(c)
	if err := h.service.Start(); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"running": true, "address": h.service.Address()})
}
func (h *PacketCaptureHandler) Stop(c *gin.Context) {
	if h.service == nil {
		c.JSON(503, gin.H{"error": "抓包服务未初始化"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := h.service.Stop(ctx); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"running": false})
}

func (h *PacketCaptureHandler) CA(c *gin.Context) {
	if h.service == nil || h.service.CA() == nil {
		c.JSON(503, gin.H{"error": "CA 未初始化"})
		return
	}
	ca := h.service.CA()
	c.JSON(200, gin.H{"cert_path": ca.CertPath(), "key_path": ca.KeyPath(), "certificate_pem": string(ca.CertPEM()), "install_windows": "certutil -addstore -user Root \"" + ca.CertPath() + "\"", "install_macos": "sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain \"" + ca.CertPath() + "\""})
}

func (h *PacketCaptureHandler) InstallWindowsCA(c *gin.Context) {
	if runtime.GOOS != "windows" || h.service == nil || h.service.CA() == nil {
		c.JSON(400, gin.H{"error": "当前平台不支持 Windows CA 安装"})
		return
	}
	cmd := exec.Command("certutil", "-addstore", "-user", "Root", h.service.CA().CertPath())
	if output, err := cmd.CombinedOutput(); err != nil {
		c.JSON(500, gin.H{"error": strings.TrimSpace(string(output))})
		return
	}
	c.JSON(200, gin.H{"message": "平台 CA 已安装到当前用户信任根"})
}

func (h *PacketCaptureHandler) DownloadCA(c *gin.Context) {
	if h.service == nil || h.service.CA() == nil {
		c.Status(503)
		return
	}
	c.Header("Content-Type", "application/x-pem-file")
	c.Header("Content-Disposition", "attachment; filename=provena-ca.crt")
	_, _ = c.Writer.Write(h.service.CA().CertPEM())
}

func (h *PacketCaptureHandler) Groups(c *gin.Context) {
	if h.db == nil {
		c.JSON(500, gin.H{"error": "数据库未初始化"})
		return
	}
	owner := h.bindOwner(c)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "500"))
	items, err := h.db.ListPacketGroups(owner, limit)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"groups": items})
}
func (h *PacketCaptureHandler) GroupPackets(c *gin.Context) {
	if h.db == nil {
		c.JSON(500, gin.H{"error": "数据库未初始化"})
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	items, err := h.db.ListCapturedPackets(c.Param("id"), h.bindOwner(c), limit)
	if err != nil {
		c.JSON(404, gin.H{"error": "分组不存在或无权访问"})
		return
	}
	c.JSON(200, gin.H{"packets": items})
}
func (h *PacketCaptureHandler) Packet(c *gin.Context) {
	if h.db == nil {
		c.JSON(500, gin.H{"error": "数据库未初始化"})
		return
	}
	item, err := h.db.GetCapturedPacket(c.Param("id"), h.bindOwner(c))
	if err != nil {
		c.JSON(404, gin.H{"error": "抓包不存在或无权访问"})
		return
	}
	c.JSON(200, item)
}

func (h *PacketCaptureHandler) bindOwner(c *gin.Context) string {
	if h == nil || h.service == nil {
		return ""
	}
	if session, ok := security.CurrentSession(c); ok {
		h.service.SetOwnerUserID(session.UserID)
		return session.UserID
	}
	return ""
}

func (h *PacketCaptureHandler) Configure(c *gin.Context) {
	var cfg config.PacketCaptureConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if h.persist != nil {
		if err := h.persist(cfg); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
	} else if err := h.service.Configure(cfg); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "抓包配置已应用", "running": h.service.Running(), "address": h.service.Address()})
}
