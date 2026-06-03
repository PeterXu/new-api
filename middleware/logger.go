package middleware

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const RouteTagKey = "route_tag"

func RouteTag(tag string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(RouteTagKey, tag)
		c.Next()
	}
}

func SetUpLogger(server *gin.Engine) {
	// 只跳过高频、低风险的只读端点
	skipPaths := []string{
		"/api/status",            // 健康检查，频繁轮询
		"/api/status/test",
		"/api/notice",            // 公告轮询
		"/api/home_page_content", // 首页内容
		"/api/user/self/groups",  // 用户组（只读）
		"/api/user/models",       // 模型列表（只读）
	}

	server.Use(gin.LoggerWithConfig(gin.LoggerConfig{
		SkipPaths: skipPaths,
		Formatter: func(param gin.LogFormatterParams) string {
			var requestID string
			if param.Keys != nil {
				requestID, _ = param.Keys[common.RequestIdKey].(string)
			}
			tag, _ := param.Keys[RouteTagKey].(string)
			if tag == "" {
				tag = "web"
			}
			return fmt.Sprintf("[GIN] %s | %s | %s | %3d | %13v | %15s | %7s %s\n",
				param.TimeStamp.Format("2006/01/02 - 15:04:05"),
				tag,
				requestID,
				param.StatusCode,
				param.Latency,
				param.ClientIP,
				param.Method,
				param.Path,
			)
		},
	}))
}
