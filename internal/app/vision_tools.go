package app

import (
	"github.com/chobits02/provena/internal/config"
	"github.com/chobits02/provena/internal/mcp"
	"github.com/chobits02/provena/internal/vision"

	"go.uber.org/zap"
)

func registerVisionTools(mcpServer *mcp.Server, cfg *config.Config, logger *zap.Logger) {
	vision.RegisterAnalyzeImageTool(mcpServer, cfg, logger)
}
