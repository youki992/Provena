package app

import (
	"context"
	"errors"
	"strings"

	"github.com/chobits02/provena/internal/handler"
	"github.com/chobits02/provena/internal/mcp"
	"github.com/chobits02/provena/internal/mcp/builtin"

	"go.uber.org/zap"
)

// registerAssetSearchTools exposes the same provider-neutral search service
// used by the web information-collection page. Pi receives this as one tool,
// so it can choose FOFA/ZoomEye/Quake/Shodan from the task and configured
// credentials without learning or passing API keys itself.
func registerAssetSearchTools(server *mcp.Server, searcher *handler.FofaHandler, logger *zap.Logger) {
	if server == nil || searcher == nil {
		return
	}
	server.RegisterTool(mcp.Tool{
		Name:             builtin.ToolSearchSpaceAssets,
		ShortDescription: "按需搜索网络空间资产",
		Description:      "在用户已配置 API 且目标范围明确时，用 FOFA、ZoomEye、Quake 或 Shodan 查询网络空间资产。只传 provider、查询语法和返回选项，不要传 API Key。查询结果必须先按当前任务 scope 过滤，再决定是否调用 create_asset；第三方、CDN、外链和无关域名不得写入资产库。未配置对应 API 时会返回需要补齐的配置字段，不要重复重试。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"provider": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"auto", "fofa", "zoomeye", "quake", "shodan"},
					"default":     "auto",
					"description": "数据源；auto 会优先选择配置完整的 FOFA，否则选择其他已配置 provider",
				},
				"query": map[string]interface{}{
					"type":        "string",
					"description": "对应数据源的查询语法，例如 FOFA domain=\"example.com\"、ZoomEye domain=\"example.com\"、Quake domain:\"example.com\"、Shodan hostname:example.com",
				},
				"size": map[string]interface{}{
					"type":        "integer",
					"minimum":     1,
					"maximum":     10000,
					"description": "返回数量；优先使用较小数量，默认 100",
				},
				"page": map[string]interface{}{
					"type":        "integer",
					"minimum":     1,
					"description": "页码，默认 1",
				},
				"fields": map[string]interface{}{
					"type":        "string",
					"description": "逗号分隔的返回字段；留空使用平台默认字段",
				},
				"full": map[string]interface{}{
					"type":        "boolean",
					"description": "是否请求更完整/最新数据；按平台配额谨慎使用",
				},
			},
			"required": []string{"query"},
		},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		provider := strArg(args, "provider")
		if provider == "" {
			provider = "auto"
		}
		request := handler.SpaceSearchRequest{
			Provider: provider,
			Query:    strArg(args, "query"),
			Size:     intArg(args, "size", 0),
			Page:     intArg(args, "page", 0),
			Fields:   strArg(args, "fields"),
			Full:     boolArg(args, "full"),
		}
		result, err := searcher.SearchSpaceAssets(ctx, request)
		if err != nil {
			if logger != nil {
				logger.Warn("Agent 网络空间资产搜索失败", zap.String("provider", request.Provider), zap.Error(err))
			}
			message := "错误: " + err.Error()
			var searchErr *handler.SpaceSearchError
			if errors.As(err, &searchErr) && searchErr != nil {
				if len(searchErr.Need) > 0 {
					message += "\n需要配置: " + strings.Join(searchErr.Need, ", ")
				}
				if len(searchErr.EnvKey) > 0 {
					message += "\n环境变量: " + strings.Join(searchErr.EnvKey, ", ")
				}
			}
			return textResult(message, true), nil
		}
		return assetJSONResult(result)
	})
}
