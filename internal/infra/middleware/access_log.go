package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	// maxBodySize 最大捕获的响应体大小 (1MB)
	maxBodySize = 1024 * 1024
)

// responseRecorder 包装 gin.ResponseWriter 以记录响应信息
type responseRecorder struct {
	gin.ResponseWriter
	written     int64
	body        *bytes.Buffer
	headers     http.Header
	captureBody bool
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	// 延迟判断是否应该捕获响应体（确保 Content-Type 已设置）
	if r.shouldCapture() && r.body != nil && r.body.Len() < maxBodySize {
		// 只捕获前 maxBodySize 字节
		remaining := maxBodySize - r.body.Len()
		if int64(len(b)) > int64(remaining) {
			r.body.Write(b[:remaining])
		} else {
			r.body.Write(b)
		}
	}
	n, err := r.ResponseWriter.Write(b)
	r.written += int64(n)
	return n, err
}

func (r *responseRecorder) WriteString(s string) (int, error) {
	// 延迟判断是否应该捕获响应体（确保 Content-Type 已设置）
	if r.shouldCapture() && r.body != nil && r.body.Len() < maxBodySize {
		// 只捕获前 maxBodySize 字节
		remaining := maxBodySize - r.body.Len()
		if int64(len(s)) > int64(remaining) {
			r.body.WriteString(s[:remaining])
		} else {
			r.body.WriteString(s)
		}
	}
	n, err := r.ResponseWriter.WriteString(s)
	r.written += int64(n)
	return n, err
}

// shouldCapture 动态判断是否应该捕获响应体
func (r *responseRecorder) shouldCapture() bool {
	// 根据实际响应的 Content-Type 动态判断
	contentType := r.ResponseWriter.Header().Get("Content-Type")
	return shouldCaptureBody(contentType)
}

func (r *responseRecorder) WriteHeader(code int) {
	// 保存响应头（在写入状态码之前，确保头信息已完整设置）
	if r.headers != nil {
		for key, values := range r.ResponseWriter.Header() {
			if len(values) > 0 {
				r.headers.Set(key, values[0])
			}
		}
	}
	r.ResponseWriter.WriteHeader(code)
}

// UsageRecorder 访问日志记录器接口
type UsageRecorder interface {
	RecordAccess(userID uuid.UUID, method, path, clientIP, userAgent string, modelName string, statusCode int, requestBytes, responseBytes int64, durationMs int64)
	RecordAccessDetailed(userID uuid.UUID, apiKeyID uuid.UUID, apiKeyName string, apiKeyPrefix string, method, path, clientIP, userAgent string, modelName string, statusCode int, requestBytes, responseBytes int64, requestHeaders map[string]string, requestBody string, responseHeaders map[string]string, responseBody string, inputTokens int, outputTokens int, durationMs int64)
}

// AccessLogMiddleware 访问日志记录中间件
// 只记录已认证用户的请求
func AccessLogMiddleware(usageService UsageRecorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. 读取完整请求体（避免截断导致的 JSON 解析错误）
		var requestBody []byte
		if c.Request.Body != nil {
			var err error
			requestBody, err = io.ReadAll(c.Request.Body)
			if err != nil {
				// 读取失败时继续处理，只是记录空请求体
				requestBody = []byte{}
			}
			// 重新设置 body，以便后续处理程序可以读取完整的 body
			c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		}

		// 2. 获取请求头
		requestHeaders := make(map[string]string)
		for key, values := range c.Request.Header {
			if len(values) > 0 {
				// 只记录关键头信息，过滤敏感信息
				if shouldRecordHeader(key) {
					requestHeaders[key] = values[0]
				}
			}
		}

		// 3. 创建增强的 response recorder
		recorder := &responseRecorder{
			ResponseWriter: c.Writer,
			body:           &bytes.Buffer{},
			headers:        make(http.Header),
			captureBody:    true, // 默认启用捕获，实际是否捕获在写入时根据 Content-Type 动态判断
		}
		c.Writer = recorder

		// 获取请求信息
		method := c.Request.Method
		path := c.Request.URL.Path
		clientIP := c.ClientIP()
		userAgent := c.Request.UserAgent()
		requestBytes := c.Request.ContentLength
		if requestBytes <= 0 {
			requestBytes = int64(len(requestBody))
		}

		// 4. 处理请求
		startTime := time.Now()
		c.Next()
		durationMs := time.Since(startTime).Milliseconds()

		// 5. 请求完成后记录访问日志（只记录已认证用户）
		var userID uuid.UUID
		var found bool

		// 尝试从标准 JWT 认证获取用户信息
		user := GetCurrentUser(c)
		if user != nil {
			userID = user.UserID
			found = true
		} else {
			// 尝试从代理端点认证获取（API Key 或 JWT）
			if uid, exists := c.Get("user_id"); exists {
				if uidUUID, ok := uid.(uuid.UUID); ok {
					userID = uidUUID
					found = true
				}
			}
		}

		if found {
			statusCode := c.Writer.Status()
			responseBytes := recorder.written

			// 获取响应头
			responseHeaders := make(map[string]string)
			for key, values := range c.Writer.Header() {
				if len(values) > 0 {
					responseHeaders[key] = values[0]
				}
			}

			// 处理响应体
			responseBody := ""
			if recorder.captureBody && recorder.body != nil {
				body := recorder.body.String()
				// 检测是否为流式响应
				if isStreamResponse(c.Writer.Header().Get("Content-Type")) {
					responseBody = parseStreamResponse([]byte(body))
				} else {
					responseBody = body
				}
			}

			// 异步记录访问日志，避免影响响应时间
			// 注意：requestBody 和 responseBody 的截断统一由 RecordAccessDetailed 内部处理
			var inputTokens, outputTokens int
			if inTokens, exists := c.Get("input_tokens"); exists {
				inputTokens, _ = inTokens.(int)
			}
			if outTokens, exists := c.Get("output_tokens"); exists {
				outputTokens, _ = outTokens.(int)
			}

			modelName := ""
			if mName, exists := c.Get("model_id"); exists {
				modelName, _ = mName.(string)
			}

			// 读取 API Key 信息
			var apiKeyID uuid.UUID
			if akid, exists := c.Get(ContextKeyAPIKeyID); exists {
				if id, ok := akid.(uuid.UUID); ok {
					apiKeyID = id
				}
			}
			apiKeyName := ""
			if akName, exists := c.Get(ContextKeyAPIKeyName); exists {
				apiKeyName, _ = akName.(string)
			}
			apiKeyPrefix := ""
			if akPrefix, exists := c.Get(ContextKeyAPIKeyPrefix); exists {
				apiKeyPrefix, _ = akPrefix.(string)
			}

			go usageService.RecordAccessDetailed(
				userID,
				apiKeyID,
				apiKeyName,
				apiKeyPrefix,
				method,
				path,
				clientIP,
				userAgent,
				modelName,
				statusCode,
				requestBytes,
				responseBytes,
				requestHeaders,
				string(requestBody),
				responseHeaders,
				responseBody,
				inputTokens,
				outputTokens,
				durationMs,
			)
		}
	}
}

// shouldRecordHeader 判断是否应该记录该请求头
func shouldRecordHeader(key string) bool {
	// 转换为小写进行比较
	keyLower := strings.ToLower(key)

	// 只记录关键头信息，避免敏感信息泄露
	allowedHeaders := []string{
		"content-type",
		"accept",
		"user-agent",
		"x-request-id",
		"x-real-ip",
		"x-forwarded-for",
		"accept-encoding",
		"accept-language",
	}

	for _, allowed := range allowedHeaders {
		if keyLower == allowed {
			return true
		}
	}
	return false
}

// shouldCaptureBody 判断是否捕获响应体
func shouldCaptureBody(contentType string) bool {
	if contentType == "" {
		return true
	}
	contentType = strings.ToLower(contentType)
	// 只捕获 JSON 和文本类型
	return strings.Contains(contentType, "application/json") ||
		strings.Contains(contentType, "text/plain") ||
		strings.Contains(contentType, "text/event-stream")
}

// isStreamResponse 判断是否为流式响应
func isStreamResponse(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "text/event-stream")
}

// parseStreamResponse 解析 SSE 流式响应，提取有效内容
// 支持 OpenAI 格式 (choices[0].delta.content) 和 Claude 格式 (delta.text)
func parseStreamResponse(body []byte) string {
	type openaiStreamToolFunction struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}

	type openaiStreamToolCall struct {
		Index    *int                     `json:"index"`
		ID       string                   `json:"id"`
		Type     string                   `json:"type"`
		Function openaiStreamToolFunction `json:"function"`
	}

	type openaiStreamDelta struct {
		Content          *string                `json:"content"`
		ReasoningContent *string                `json:"reasoning_content"`
		ToolCalls        []openaiStreamToolCall `json:"tool_calls"`
	}

	type openaiStreamChoice struct {
		Index        int               `json:"index"`
		Text         *string           `json:"text"`
		Delta        openaiStreamDelta `json:"delta"`
		FinishReason *string           `json:"finish_reason"`
	}

	type openaiStreamEvent struct {
		Choices []openaiStreamChoice `json:"choices"`
	}

	type responsesStreamEvent struct {
		Type  string  `json:"type"`
		Delta *string `json:"delta"`
		Item  *struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"item"`
	}

	type anthropicContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	type anthropicStreamDelta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
	}

	type anthropicStreamEvent struct {
		Type         string                 `json:"type"`
		Index        *int                   `json:"index"`
		Delta        *anthropicStreamDelta  `json:"delta"`
		ContentBlock *anthropicContentBlock `json:"content_block"`
	}

	var contents []string
	var toolCalls []string
	var inToolCall bool

	bodyStr := strings.ReplaceAll(string(body), "\r\n", "\n")
	lines := strings.Split(bodyStr, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		var data string
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		} else if strings.HasPrefix(line, "data:") {
			data = strings.TrimPrefix(line, "data:")
		} else {
			continue
		}
		data = strings.TrimSpace(data)

		if data == "[DONE]" {
			break
		}

		// 1. 尝试解析为 OpenAI (Chat / Text Completions) 格式
		var oe openaiStreamEvent
		if err := json.Unmarshal([]byte(data), &oe); err == nil && len(oe.Choices) > 0 {
			choice := oe.Choices[0]
			delta := choice.Delta

			if choice.Text != nil && *choice.Text != "" {
				contents = append(contents, *choice.Text)
			}
			// 提取普通文本内容
			if delta.Content != nil && *delta.Content != "" {
				contents = append(contents, *delta.Content)
			}
			// 提取思考/推理内容
			if delta.ReasoningContent != nil && *delta.ReasoningContent != "" {
				contents = append(contents, *delta.ReasoningContent)
			}

			// 提取 Tool Calls
			if len(delta.ToolCalls) > 0 {
				for _, tc := range delta.ToolCalls {
					if tc.Function.Name != "" {
						if inToolCall {
							toolCalls = append(toolCalls, ")")
						}
						toolCalls = append(toolCalls, fmt.Sprintf("\n[Tool Call]: %s(", tc.Function.Name))
						inToolCall = true
					}
					if tc.Function.Arguments != "" {
						toolCalls = append(toolCalls, tc.Function.Arguments)
					}
				}
			}

			if choice.FinishReason != nil && *choice.FinishReason == "tool_calls" {
				if inToolCall {
					toolCalls = append(toolCalls, ")")
					inToolCall = false
				}
			}
			continue
		}

		// 2. 尝试解析为 Responses 格式
		var re responsesStreamEvent
		if err := json.Unmarshal([]byte(data), &re); err == nil && strings.HasPrefix(re.Type, "response.") {
			if re.Type == "response.output_item.added" && re.Item != nil && re.Item.Type == "function_call" {
				if inToolCall {
					toolCalls = append(toolCalls, ")")
				}
				toolCalls = append(toolCalls, fmt.Sprintf("\n[Tool Call]: %s(", re.Item.Name))
				inToolCall = true
			} else if re.Type == "response.function_call_arguments.delta" && re.Delta != nil {
				toolCalls = append(toolCalls, *re.Delta)
			} else if re.Type == "response.function_call_arguments.done" {
				if inToolCall {
					toolCalls = append(toolCalls, ")")
					inToolCall = false
				}
			} else if re.Delta != nil && *re.Delta != "" {
				contents = append(contents, *re.Delta)
			}
			continue
		}

		// 2. 尝试解析为 Anthropic 格式
		var ae anthropicStreamEvent
		if err := json.Unmarshal([]byte(data), &ae); err == nil {
			if ae.Type == "content_block_start" && ae.ContentBlock != nil {
				if ae.ContentBlock.Type == "tool_use" && ae.ContentBlock.Name != "" {
					if inToolCall {
						toolCalls = append(toolCalls, ")")
					}
					toolCalls = append(toolCalls, fmt.Sprintf("\n[Tool Call]: %s(", ae.ContentBlock.Name))
					inToolCall = true
				}
			} else if ae.Type == "content_block_delta" && ae.Delta != nil {
				if ae.Delta.Type == "text_delta" && ae.Delta.Text != "" {
					contents = append(contents, ae.Delta.Text)
				}
				if ae.Delta.Type == "thinking_delta" && ae.Delta.Thinking != "" {
					contents = append(contents, "[Thinking]: "+ae.Delta.Thinking)
				}
				if ae.Delta.Type == "input_json_delta" && ae.Delta.PartialJSON != "" {
					toolCalls = append(toolCalls, ae.Delta.PartialJSON)
				}
			} else if ae.Type == "content_block_stop" {
				if inToolCall {
					toolCalls = append(toolCalls, ")")
					inToolCall = false
				}
			}
			continue
		}
	}

	if inToolCall {
		toolCalls = append(toolCalls, ")")
	}

	result := strings.Join(contents, "")
	if len(toolCalls) > 0 {
		combinedTools := strings.Join(toolCalls, "")
		result += "\n\n" + strings.TrimSpace(combinedTools)
	}

	// 如果没有提取到任何内容，返回原始 body（可能是未知格式）
	if result == "" && len(body) > 0 {
		return string(body)
	}

	return result
}
