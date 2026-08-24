package proxy

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"modelgate/internal/domain/quota"
	"modelgate/internal/domain/usage"
	"modelgate/internal/infra/logger"
	"modelgate/internal/infra/utils"
	"modelgate/internal/repository"
)

// Proxy LLM 代理
type Proxy struct {
	lb            *RoundRobinBalancer
	quotaService  *quota.Service
	usageService  *usage.Service
	httpClient    *http.Client
	modelStore    *entity.ModelStore
	backendStore  *entity.BackendStore
	userStore     *entity.UserStore
	trafficDumper *logger.TrafficDumper
}

func NewProxy(lb *RoundRobinBalancer, quotaService *quota.Service, usageService *usage.Service, modelStore *entity.ModelStore, backendStore *entity.BackendStore, userStore *entity.UserStore) *Proxy {
	return &Proxy{
		lb:           lb,
		quotaService: quotaService,
		usageService: usageService,
		httpClient:   &http.Client{Timeout: 30 * time.Minute},
		modelStore:   modelStore,
		backendStore: backendStore,
		userStore:    userStore,
	}
}

// SetTrafficDumper 设置原始流量调试日志组件
func (p *Proxy) SetTrafficDumper(dumper *logger.TrafficDumper) {
	p.trafficDumper = dumper
}

// OpenAIRequest OpenAI 兼容的请求格式
type OpenAIRequest struct {
	Model    string                   `json:"model"`
	Messages []map[string]interface{} `json:"messages"`
	Stream   bool                     `json:"stream,omitempty"`
}

// OpenAIResponse OpenAI 兼容的响应格式
type OpenAIResponse struct {
	ID      string                   `json:"id"`
	Object  string                   `json:"object"`
	Created int64                    `json:"created"`
	Model   string                   `json:"model"`
	Choices []map[string]interface{} `json:"choices"`
	Usage   *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

// StreamResponse 流式响应格式
type StreamResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []StreamChoice `json:"choices"`
	Usage   *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

type StreamChoice struct {
	Index        int                    `json:"index"`
	Delta        map[string]interface{} `json:"delta"`
	FinishReason *string                `json:"finish_reason"`
}

// OpenAIRequestHeader 优化后的轻量级请求解析器，避免对 messages 进行全量 unmarshal
type OpenAIRequestHeader struct {
	Model               string
	Stream              bool
	MaxTokens           *int
	MaxCompletionTokens *int
	Messages            []ChatMessage
	Tools               []json.RawMessage
	RawFields           map[string]json.RawMessage
}

type ChatMessage struct {
	Role             string                   `json:"role"`
	Content          json.RawMessage          `json:"content"`
	Name             *string                  `json:"name,omitempty"`
	ToolCallID       *string                  `json:"tool_call_id,omitempty"`
	ToolCalls        []map[string]interface{} `json:"tool_calls,omitempty"`
	ReasoningContent *string                  `json:"reasoning_content,omitempty"`
}

func (r *OpenAIRequestHeader) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &r.RawFields); err != nil {
		return err
	}

	if modelRaw, exists := r.RawFields["model"]; exists {
		_ = json.Unmarshal(modelRaw, &r.Model)
	}
	if streamRaw, exists := r.RawFields["stream"]; exists {
		_ = json.Unmarshal(streamRaw, &r.Stream)
	}
	if maxTokensRaw, exists := r.RawFields["max_tokens"]; exists {
		_ = json.Unmarshal(maxTokensRaw, &r.MaxTokens)
	}
	if maxCompletionTokensRaw, exists := r.RawFields["max_completion_tokens"]; exists {
		_ = json.Unmarshal(maxCompletionTokensRaw, &r.MaxCompletionTokens)
	}
	if messagesRaw, exists := r.RawFields["messages"]; exists {
		_ = json.Unmarshal(messagesRaw, &r.Messages)
	}
	if toolsRaw, exists := r.RawFields["tools"]; exists {
		_ = json.Unmarshal(toolsRaw, &r.Tools)
	}
	return nil
}

func (r *OpenAIRequestHeader) MarshalJSON() ([]byte, error) {
	if r.RawFields == nil {
		r.RawFields = make(map[string]json.RawMessage)
	}

	if r.Model != "" {
		b, _ := json.Marshal(r.Model)
		r.RawFields["model"] = b
	} else {
		delete(r.RawFields, "model")
	}

	bStream, _ := json.Marshal(r.Stream)
	r.RawFields["stream"] = bStream

	if r.MaxTokens != nil {
		b, _ := json.Marshal(r.MaxTokens)
		r.RawFields["max_tokens"] = b
	} else {
		delete(r.RawFields, "max_tokens")
	}

	if r.MaxCompletionTokens != nil {
		b, _ := json.Marshal(r.MaxCompletionTokens)
		r.RawFields["max_completion_tokens"] = b
	} else {
		delete(r.RawFields, "max_completion_tokens")
	}

	if len(r.Messages) > 0 {
		b, _ := json.Marshal(r.Messages)
		r.RawFields["messages"] = b
	} else {
		delete(r.RawFields, "messages")
	}

	if len(r.Tools) > 0 {
		b, _ := json.Marshal(r.Tools)
		r.RawFields["tools"] = b
	} else {
		delete(r.RawFields, "tools")
	}

	return json.Marshal(r.RawFields)
}

func (r *OpenAIRequestHeader) ToMap() map[string]interface{} {
	b, _ := json.Marshal(r)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	return m
}

func (r *OpenAIRequestHeader) EstimateTokens() int {
	var contentBuilder strings.Builder

	for _, msg := range r.Messages {
		if len(msg.Content) > 0 {
			var strContent string
			if err := json.Unmarshal(msg.Content, &strContent); err == nil {
				contentBuilder.WriteString(strContent)
			} else {
				var arrayContent []map[string]interface{}
				if err := json.Unmarshal(msg.Content, &arrayContent); err == nil {
					for _, block := range arrayContent {
						if bType, _ := block["type"].(string); bType == "text" {
							if bText, _ := block["text"].(string); bText != "" {
								contentBuilder.WriteString(bText)
							}
						}
					}
				}
			}
		}
	}

	for _, toolObj := range r.Tools {
		contentBuilder.Write(toolObj)
	}

	return utils.EstimateTokens(contentBuilder.String())
}

func (r *OpenAIRequestHeader) InjectParams(params map[string]interface{}) {
	if len(params) == 0 {
		return
	}
	if r.RawFields == nil {
		r.RawFields = make(map[string]json.RawMessage)
	}
	for k, v := range params {
		switch k {
		case "model":
			if r.Model == "" {
				if strVal, ok := v.(string); ok {
					r.Model = strVal
				}
			}
		case "stream":
			// skip stream injection
		case "max_tokens":
			if r.MaxTokens == nil && r.MaxCompletionTokens == nil {
				if floatVal, ok := v.(float64); ok {
					intVal := int(floatVal)
					r.MaxTokens = &intVal
				}
			}
		case "max_completion_tokens":
			if r.MaxTokens == nil && r.MaxCompletionTokens == nil {
				if floatVal, ok := v.(float64); ok {
					intVal := int(floatVal)
					r.MaxCompletionTokens = &intVal
				}
			}
		default:
			if _, exists := r.RawFields[k]; !exists {
				if b, err := json.Marshal(v); err == nil {
					r.RawFields[k] = b
				}
			}
		}
	}
}

// BackendRequest 后端请求参数（纯输入，由协议 Handler 构造）
type BackendRequest struct {
	ModelID     string
	UserID      uuid.UUID
	APIKeyID    uuid.UUID
	RequestBody []byte
	IsStream    bool
	ClientIP    string
	UserAgent   string
}

// BackendResponse 后端响应
type BackendResponse struct {
	Body       io.ReadCloser
	StatusCode int
	BackendID  string
}

// ExecuteCoreWorkflow 执行核心代理工作流（复用逻辑）
// 支持请求/响应转换，用于实现多协议支持
func (p *Proxy) ExecuteCoreWorkflow(
	c *gin.Context,
	req *BackendRequest,
	proto Protocol,
) {
	pctx := p.NewProxyContext(c, req, proto)

	// 1. 认证用户并检查配额
	if !p.authenticateAndCheckQuota(pctx) {
		return
	}

	// 2. 选择后端（内部已获取并发许可）
	backend := p.selectBackend(pctx)
	if backend == nil {
		return
	}

	// 3. 确保请求完成后释放并发许可（包括 streaming 完成、错误、客户端断开等任何情况）
	defer p.lb.ReleaseBackend(pctx.BackendID)

	// 4. 准备并发送请求
	resp := p.prepareAndSendRequest(pctx, backend)
	if resp == nil {
		return
	}

	// 5. 处理响应
	p.dispatchResponse(pctx, resp)
}

// authenticateAndCheckQuota 获取用户信息并检查配额
// 返回 false 表示请求已被终止（错误已发送给客户端）
func (p *Proxy) authenticateAndCheckQuota(pctx *ProxyContext) bool {
	req := pctx.Request

	user, err := p.userStore.GetByID(req.UserID)
	if err != nil {
		pctx.SendError(http.StatusInternalServerError, "api_error", "failed to get user info")
		return false
	}
	if user == nil {
		pctx.SendError(http.StatusUnauthorized, "invalid_request_error", "user not found")
		return false
	}

	pctx.User = user

	// 检查配额
	policies := []string(user.QuotaPolicies)
	if len(policies) == 0 {
		if user.QuotaPolicy != "" {
			policies = []string{user.QuotaPolicy}
		} else {
			policies = []string{"default"}
		}
	}

	quotaResult, err := p.quotaService.CheckQuotaMulti(req.UserID, policies, req.ModelID)
	if err != nil {
		pctx.SendError(http.StatusInternalServerError, "api_error", "quota check failed")
		return false
	}

	// 当指定的模型不被允许时，降级使用默认模型重试
	if !quotaResult.Allowed && quotaResult.Reason == "model not allowed" && quotaResult.DefaultModel != "" {
		req.ModelID = quotaResult.DefaultModel
		quotaResult, err = p.quotaService.CheckQuotaMulti(req.UserID, policies, req.ModelID)
		if err != nil {
			pctx.SendError(http.StatusInternalServerError, "api_error", "quota check failed")
			return false
		}
	}

	if !quotaResult.Allowed {
		pctx.SendError(http.StatusTooManyRequests, "rate_limit_error", quotaResult.Reason)
		return false
	}

	// CheckQuota 只是校验限额，这里需要真实累加内存中的 rate limit 计数器
	_ = p.quotaService.IncrementRate(req.UserID, quotaResult.RateLimitWindow)
	pctx.DefaultModel = quotaResult.DefaultModel
	return true
}

// selectBackend 通过负载均衡选择后端，并更新 pctx 上的 BackendID 和 ModelID
// Next() 内部使用最少连接策略，选择并发数最低的后端，并原子性地获取并发许可
// 返回 nil 表示无可用后端（错误已发送给客户端）
func (p *Proxy) selectBackend(pctx *ProxyContext) *Backend {
	req := pctx.Request

	backend, actualModelID, ok := p.lb.Next(req.ModelID, pctx.DefaultModel)
	if !ok {
		pctx.RecordErrorUsage(http.StatusTooManyRequests, "all backends at concurrency capacity")
		pctx.SendError(http.StatusTooManyRequests, "rate_limit_error", "all backends for model "+req.ModelID+" are at concurrency capacity, please retry later")
		return nil
	}

	req.ModelID = actualModelID
	pctx.GinCtx.Set("model_id", actualModelID)
	pctx.BackendID = backend.ID

	return backend
}

// prepareAndSendRequest 准备请求体、构造 HTTP 请求并发送到后端
// 返回 nil 表示请求失败（错误已发送给客户端）
func (p *Proxy) prepareAndSendRequest(pctx *ProxyContext, backend *Backend) *http.Response {
	req := pctx.Request
	c := pctx.GinCtx

	// 获取模型配置并处理请求体（所有修改在 map 上完成，只序列化一次）
	modelConfig, _ := p.modelStore.GetByID(req.ModelID)

	if modelConfig != nil && len(modelConfig.ModelParams) > 0 {
		pctx.Payload.InjectParams(modelConfig.ModelParams)
	}

	// 计算请求的 InputTokens（只计算一次，复用于 adjustMaxTokens 和日志记录）
	pctx.InputTokens = pctx.Payload.EstimateTokens()

	if modelConfig != nil && modelConfig.ContextWindow > 0 {
		adjustMaxTokens(pctx.Payload, modelConfig.ContextWindow, pctx.InputTokens)
	}

	// 自动为 OpenAI 兼容请求注入缓存的 Gemini thought_signature
	injectOpenAIThoughtSignatures(pctx.Payload)

	// 替换 model 名称：优先使用后端配置的模型名，否则使用负载均衡解析后的模型 ID
	if backend.ModelName != "" {
		pctx.Payload.Model = backend.ModelName
	} else {
		pctx.Payload.Model = req.ModelID
	}

	// 序列化请求体（整个流程只序列化这一次）
	requestBody, err := pctx.MarshalRequestBody()
	if err != nil {
		pctx.SendError(http.StatusInternalServerError, "api_error", "failed to marshal request body")
		return nil
	}

	// Dump 阶段 2（实际发送给后端的请求体，包含 model 替换、参数注入、max_tokens 裁剪）
	pctx.DumpTraffic(logger.Stage2ConvertedRequest, requestBody, false)

	// 构造目标 URL
	baseURL := strings.TrimSuffix(backend.URL, "/")
	var url string
	if strings.HasSuffix(baseURL, "/openai") {
		url = baseURL + "/chat/completions"
	} else {
		url = baseURL + "/v1/chat/completions"
	}
	proxyReq, err := http.NewRequest(c.Request.Method, url, bytes.NewReader(requestBody))
	if err != nil {
		pctx.SendError(http.StatusInternalServerError, "api_error", "failed to create proxy request")
		return nil
	}

	// 复制请求头（排除 Accept-Encoding，避免后端返回 gzip 压缩响应）
	for key, values := range c.Request.Header {
		if strings.ToLower(key) == "accept-encoding" {
			continue
		}
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}

	// 添加后端认证
	if backend.APIKey != "" {
		proxyReq.Header.Set("Authorization", "Bearer "+backend.APIKey)
	}

	// 注入自定义 header
	if modelConfig != nil && len(modelConfig.ModelParams) > 0 {
		for key, value := range modelConfig.ModelParams {
			if strings.HasPrefix(key, "__") && strings.HasSuffix(key, "__") {
				headerName := convertHeaderName(key)
				if strValue, ok := value.(string); ok {
					proxyReq.Header.Set(headerName, strValue)
				}
			}
		}
	}

	proxyReq.ContentLength = int64(len(requestBody))

	// 发送请求
	resp, err := p.httpClient.Do(proxyReq)
	if err != nil {
		p.lb.MarkFailed(backend.ID)
		if netErr, ok := err.(interface{ Timeout() bool }); ok && netErr.Timeout() {
			pctx.SendError(http.StatusGatewayTimeout, "api_error", "backend request timeout")
		} else {
			pctx.SendError(http.StatusServiceUnavailable, "api_error", "backend unavailable: "+err.Error())
		}
		return nil
	}

	p.lb.MarkSuccess(backend.ID)
	return resp
}

// dispatchResponse 根据响应状态码和内容类型分派到对应的处理器
func (p *Proxy) dispatchResponse(pctx *ProxyContext, resp *http.Response) {
	// 透传非 200 状态码
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		p.handleErrorResponse(pctx, resp)
		return
	}

	// 检查后端实际响应的内容类型
	contentType := resp.Header.Get("Content-Type")
	isStreamResponse := pctx.Request.IsStream && (strings.Contains(contentType, "text/event-stream") || strings.Contains(contentType, "application/x-ndjson"))

	// 根据是否流式响应选择处理方式
	if isStreamResponse {
		p.handleStreamResponse(pctx, resp)
	} else {
		defer resp.Body.Close()
		p.handleNormalResponse(pctx, resp)
	}
}

// handleErrorResponse 处理后端返回的非 200 状态码
func (p *Proxy) handleErrorResponse(pctx *ProxyContext, resp *http.Response) {
	respBody, _ := io.ReadAll(resp.Body)

	outputTokens := utils.EstimateTokens(string(respBody))
	latency := pctx.Latency()

	// 错误场景只记录日志，不扣除 Token 配额
	pctx.GinCtx.Set("input_tokens", pctx.InputTokens)
	pctx.GinCtx.Set("output_tokens", outputTokens)
	p.usageService.RecordUsageDetailed(pctx.buildUsageRecord(resp.StatusCode, pctx.InputTokens, outputTokens, latency, string(respBody), latency))

	// 尝试从后端返回的 OpenAI 错误中提取真正的错误信息
	errType := "api_error"
	errMsg := string(respBody)
	var backendErr struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &backendErr); err == nil && backendErr.Error.Message != "" {
		errType = backendErr.Error.Type
		if errType == "" {
			errType = "api_error"
		}
		errMsg = backendErr.Error.Message
	}

	var finalRespBody []byte
	if pctx.Proto != nil {
		finalRespBody = pctx.Proto.BuildErrorResponse(errType, errMsg)
		pctx.GinCtx.Data(resp.StatusCode, "application/json", finalRespBody)
	} else {
		finalRespBody = respBody
		pctx.GinCtx.Data(resp.StatusCode, resp.Header.Get("Content-Type"), finalRespBody)
	}

	// 在发生 HTTP 错误时，也必须记录阶段 3 和 4 的 Dump
	pctx.DumpTraffic(fmt.Sprintf("3_%d_backend_response.txt", resp.StatusCode), respBody, false)
	pctx.DumpTraffic(fmt.Sprintf("4_%d_converted_response.txt", resp.StatusCode), finalRespBody, false)
}

// handleNormalResponse 处理非流式响应（带转换）
func (p *Proxy) handleNormalResponse(pctx *ProxyContext, resp *http.Response) {
	inputTokens := pctx.InputTokens

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		pctx.RecordErrorUsage(http.StatusBadGateway, "failed to read backend response")
		pctx.GinCtx.JSON(http.StatusBadGateway, gin.H{"error": "failed to read backend response"})
		return
	}

	// 检查是否需要解压 gzip 响应
	decompressed := false
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(bytes.NewReader(respBody))
		if err != nil {
			logger.Warnw("failed to create gzip reader", "error", err)
		} else {
			defer gzipReader.Close()
			decompressedBody, err := io.ReadAll(gzipReader)
			if err != nil {
				logger.Warnw("failed to decompress gzip response", "error", err)
			} else {
				respBody = decompressedBody
				decompressed = true
			}
		}
	}

	// 使用协议接口转换响应并获取精准 Token
	var preciseInput, preciseOutput int
	var convertedRespBody []byte
	if pctx.Proto != nil {
		var err error
		convertedRespBody, preciseInput, preciseOutput, err = pctx.Proto.FormatResponse(respBody)
		if err != nil {
			pctx.GinCtx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to convert response: " + err.Error()})
			return
		}
	} else {
		convertedRespBody = respBody
	}

	// 针对 OpenAI 兼容接口（不管是直接代理还是使用 Protocol 的兼容接口，如 /v1/chat/completions），在此提取并缓存 thought_signature
	if pctx.Proto == nil || strings.Contains(pctx.GinCtx.Request.URL.Path, "/chat/completions") {
		cacheThoughtSignaturesFromResponse(respBody)
	}

	pctx.DumpTraffic(fmt.Sprintf("3_%d_backend_response.txt", resp.StatusCode), respBody, false)
	pctx.DumpTraffic(fmt.Sprintf("4_%d_converted_response.txt", resp.StatusCode), convertedRespBody, false)

	// 计算最终 Token（优先使用精确 Token）
	if preciseInput > 0 {
		inputTokens = preciseInput
	}
	outputTokens := preciseOutput
	if outputTokens == 0 {
		outputTokens = utils.EstimateTokens(string(convertedRespBody))
	}

	latency := pctx.Latency()
	pctx.RecordUsage(resp.StatusCode, inputTokens, outputTokens, latency, string(convertedRespBody), latency)

	// 只有在成功解压后才删除 Content-Encoding header
	if decompressed {
		resp.Header.Del("Content-Encoding")
	}

	// 设置 Content-Type（确保中间件能正确捕获）
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json" // 默认 Content-Type
	}
	pctx.GinCtx.Header("Content-Type", contentType)

	// 返回响应
	pctx.GinCtx.Data(resp.StatusCode, contentType, convertedRespBody)
}

// handleStreamResponse 处理流式响应（带转换）
func (p *Proxy) handleStreamResponse(pctx *ProxyContext, resp *http.Response) {
	c := pctx.GinCtx
	inputTokens := pctx.InputTokens

	defer resp.Body.Close()
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // 告知 Nginx 不要缓存响应
	c.Status(resp.StatusCode)

	pingMessage := ": ping\n\n"
	if pctx.Proto != nil && pctx.Proto.PingMessage() != "" {
		pingMessage = pctx.Proto.PingMessage()
	}

	// 立即发送一个 SSE 注释并 Flush，确保客户端收到 Header，防止首字节超时
	c.Writer.WriteString(pingMessage)
	c.Writer.Flush()

	// 使用带 timeout 的 context 处理流式响应，设置为 1 小时以支持超长生成
	ctx, cancel := context.WithTimeout(c.Request.Context(), 1*time.Hour)
	defer cancel()

	// 使用 mutex 保护并发写入 c.Writer (主循环和心跳协程)
	var writeMu sync.Mutex

	// 设置心跳计时器，每 30 秒发送一个 SSE 注释，防止中间代理因闲置断开连接
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// 启动心跳协程
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				writeMu.Lock()
				if pingMessage != "" {
					_, _ = c.Writer.WriteString(pingMessage)
				} else {
					_, _ = c.Writer.WriteString(": keep-alive\n\n")
				}
				c.Writer.Flush()
				writeMu.Unlock()
			}
		}
	}()

	// 处理 gzip 压缩的流式响应
	var reader *bufio.Reader
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			logger.Errorw("Failed to create gzip reader", "error", err)
			return
		}
		defer gzipReader.Close()
		reader = bufio.NewReader(gzipReader)
	} else {
		reader = bufio.NewReader(resp.Body)
	}

	// 创建该流的状态跟踪器
	streamState := make(map[string]interface{})
	var fullCollectedText strings.Builder
	var preciseInputTokens, preciseOutputTokens int
	var firstTokenOnce sync.Once
	var ttftMs int64

	// 使用 defer 确保无论流式循环如何退出（正常 EOF、错误、ctx 取消），都记录 Token
	defer func() {
		outputTokens := preciseOutputTokens
		if outputTokens == 0 {
			outputTokens = utils.EstimateTokens(fullCollectedText.String())
		}

		finalInputTokens := inputTokens
		if preciseInputTokens > 0 {
			finalInputTokens = preciseInputTokens
		}

		latency := pctx.Latency()
		pctx.RecordUsage(resp.StatusCode, finalInputTokens, outputTokens, latency, fullCollectedText.String(), ttftMs)
	}()

	dumpFilename3 := fmt.Sprintf("3_%d_backend_response.txt", resp.StatusCode)
	dumpFilename4 := fmt.Sprintf("4_%d_converted_response.txt", resp.StatusCode)

	for {
		select {
		case <-ctx.Done():
			logger.Warn("Stream processing timeout or cancelled")
			return // defer 会确保记录 Token
		default:
		}

		line, err := reader.ReadString('\n')
		firstTokenOnce.Do(func() {
			ttftMs = pctx.Latency()
		})

		if err != nil {
			if err == io.EOF {
				break
			}
			logger.Errorw("Failed to read stream", "error", err)
			break
		}

		// 转换每一行
		if pctx.Proto != nil {
			converted, inToks, outToks, contentDelta, err := pctx.Proto.FormatStreamLine(line, streamState)
			if inToks > 0 {
				preciseInputTokens = inToks
			}
			if outToks > 0 {
				preciseOutputTokens = outToks
			}

			// 解析并缓存 OpenAI 兼容接口流中的 thought_signature
			if strings.Contains(pctx.GinCtx.Request.URL.Path, "/chat/completions") {
				cacheThoughtSignaturesFromStreamLine(line, streamState)
			}

			// Dump Stage 3 & 4
			pctx.DumpTraffic(dumpFilename3, []byte(line), true)
			if err == nil {
				pctx.DumpTraffic(dumpFilename4, []byte(converted), true)
			} else {
				pctx.DumpTraffic(dumpFilename4, []byte(line), true)
			}

			writeMu.Lock()
			if err != nil {
				_, _ = c.Writer.WriteString(line)
				content, _, _ := ParseOpenAISSE(line)
				fullCollectedText.WriteString(content)
			} else {
				_, _ = c.Writer.WriteString(converted)
				fullCollectedText.WriteString(contentDelta)
			}
			c.Writer.Flush()
			writeMu.Unlock()
		} else {
			// Dump Stage 3 & 4 for direct proxy
			pctx.DumpTraffic(dumpFilename3, []byte(line), true)
			pctx.DumpTraffic(dumpFilename4, []byte(line), true)

			// 解析并缓存直通代理流中的 thought_signature
			cacheThoughtSignaturesFromStreamLine(line, streamState)

			writeMu.Lock()
			_, _ = c.Writer.WriteString(line)
			c.Writer.Flush()
			writeMu.Unlock()
			contentDelta, inToks, outToks := ParseOpenAISSE(line)
			if inToks > 0 {
				preciseInputTokens = inToks
			}
			if outToks > 0 {
				preciseOutputTokens = outToks
			}
			fullCollectedText.WriteString(contentDelta)
		}
	}
}

// ParseOpenAISSE 解析 OpenAI SSE 格式的行
// 返回:
// - contentText: 提取的文本内容（用于估算Token）
// - preciseInputTokens: 如果包含 Usage，提取精确 Input Tokens
// - preciseOutputTokens: 如果包含 Usage，提取精确 Output Tokens
func ParseOpenAISSE(line string) (string, int, int) {
	var result strings.Builder
	var preciseInput, preciseOutput int

	// 处理可能包含多个 SSE 事件的字符串
	for _, segment := range strings.Split(line, "\n") {
		segment = strings.TrimSpace(segment)

		// 兼容 "data: {...}" 和 "data:{...}" 两种格式
		var jsonStr string
		if strings.HasPrefix(segment, "data: ") {
			jsonStr = strings.TrimPrefix(segment, "data: ")
		} else if strings.HasPrefix(segment, "data:") {
			jsonStr = strings.TrimPrefix(segment, "data:")
		} else {
			continue
		}

		jsonStr = strings.TrimSpace(jsonStr)
		if jsonStr == "[DONE]" || jsonStr == "" {
			continue
		}

		// 尝试 OpenAI 格式
		var streamResp StreamResponse
		if err := json.Unmarshal([]byte(jsonStr), &streamResp); err == nil {
			// 提取 Usage
			if streamResp.Usage != nil {
				if streamResp.Usage.PromptTokens > 0 {
					preciseInput = streamResp.Usage.PromptTokens
				}
				if streamResp.Usage.CompletionTokens > 0 {
					preciseOutput = streamResp.Usage.CompletionTokens
				}
			}

			// 提取 Content
			if len(streamResp.Choices) > 0 {
				if content, ok := streamResp.Choices[0].Delta["content"].(string); ok {
					result.WriteString(content)
				}
				// 提取 reasoning_content（思考模型如 Kimi、DeepSeek R1 等使用）
				if reasoning, ok := streamResp.Choices[0].Delta["reasoning_content"].(string); ok {
					result.WriteString(reasoning)
				}
				// 提取 tool_calls arguments
				if toolCalls, ok := streamResp.Choices[0].Delta["tool_calls"].([]interface{}); ok {
					for _, tc := range toolCalls {
						if tcMap, ok := tc.(map[string]interface{}); ok {
							if fn, ok := tcMap["function"].(map[string]interface{}); ok {
								if args, ok := fn["arguments"].(string); ok {
									result.WriteString(args)
								}
							}
						}
					}
				}
			}
		}
	}
	return result.String(), preciseInput, preciseOutput
}

func (p *Proxy) HandleListModels(c *gin.Context) {
	models, err := p.modelStore.ListEnabled()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "api_error",
				"message": "failed to list models",
			},
		})
		return
	}

	var data []map[string]interface{}
	for _, m := range models {
		data = append(data, map[string]interface{}{
			"id":       m.ID,
			"object":   "model",
			"created":  m.CreatedAt.Unix(),
			"owned_by": "modelgate",
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   data,
	})
}

// injectModelParams 将模型参数注入已解析的请求 payload
// 注意：不覆盖用户已经传入的参数
func injectModelParams(payload map[string]interface{}, params map[string]interface{}) {
	for key, value := range params {
		if _, exists := payload[key]; !exists {
			payload[key] = value
		}
	}
}

// convertHeaderName 将 __user_agent__ 转换为 User-Agent
// 规则：去掉前后的 __，将下划线替换为连字符，每个单词首字母大写
func convertHeaderName(key string) string {
	// 去掉前后的 __
	name := strings.TrimPrefix(key, "__")
	name = strings.TrimSuffix(name, "__")

	// 如果是 "header_xxx" 格式，提取后半部分
	name = strings.TrimPrefix(name, "header_")

	// 将下划线分割的单词转换为首字母大写，然后用连字符连接
	// user_agent -> User-Agent
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
		}
	}
	return strings.Join(parts, "-")
}

// adjustMaxTokens 检查并裁剪 max_tokens 或 max_completion_tokens
// 直接操作已解析的 payload struct，inputTokens 由调用方预先计算并传入
func adjustMaxTokens(reqHeader *OpenAIRequestHeader, contextWindow int, inputTokens int) {
	if reqHeader == nil {
		return
	}

	var maxTokens int
	var tokenKey string

	if reqHeader.MaxTokens != nil {
		maxTokens = *reqHeader.MaxTokens
		tokenKey = "max_tokens"
	} else if reqHeader.MaxCompletionTokens != nil {
		maxTokens = *reqHeader.MaxCompletionTokens
		tokenKey = "max_completion_tokens"
	}

	if tokenKey == "" {
		return
	}

	if inputTokens >= contextWindow {
		newVal := 100
		if tokenKey == "max_tokens" {
			reqHeader.MaxTokens = &newVal
		} else {
			reqHeader.MaxCompletionTokens = &newVal
		}
	} else if maxTokens <= 0 || inputTokens+maxTokens > contextWindow {
		newMax := contextWindow - inputTokens
		if newMax < 100 {
			newMax = 100
		}
		if tokenKey == "max_tokens" {
			reqHeader.MaxTokens = &newMax
		} else {
			reqHeader.MaxCompletionTokens = &newMax
		}
	}
}
