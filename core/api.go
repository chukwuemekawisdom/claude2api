package core

import (
	"bufio"
	"claude2api/logger"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/imroc/req/v3"
)

// OpenAISrteamResponse 定义 OpenAI 的流式响应结构
type OpenAISrteamResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []StreamChoice `json:"choices"`
}

// Choice 结构表示 OpenAI 返回的单个选项
type StreamChoice struct {
	Index        int         `json:"index"`
	Delta        Delta       `json:"delta"`
	Logprobs     interface{} `json:"logprobs"`
	FinishReason interface{} `json:"finish_reason"`
}

type NoStreamChoice struct {
	Index        int         `json:"index"`
	Message      Message     `json:"message"`
	Logprobs     interface{} `json:"logprobs"`
	FinishReason string      `json:"finish_reason"`
}

// Delta 结构用于存储返回的文本内容
type Delta struct {
	Content string `json:"content"`
}
type Message struct {
	Role       string        `json:"role"`
	Content    string        `json:"content"`
	refusal    interface{}   `json:"refusal"`
	annotation []interface{} `json:"annotation"`
}

type OpenAIResponse struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Created int64            `json:"created"`
	Model   string           `json:"model"`
	Choices []NoStreamChoice `json:"choices"`
	Usage   Usage            `json:"usage"`
}
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Client struct {
	sessionKey   string
	orgID        string
	client       *req.Client
	defaultAttrs map[string]interface{}
}

// {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" 你好！很"}      }
type ResponseEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		THINKING string `json:"thinking"`
	} `json:"delta"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// NewClient creates a new Claude API client
func NewClient(sessionKey string, proxy string) *Client {
	client := req.C().ImpersonateChrome().SetTimeout(time.Minute * 5)
	client.Transport.SetResponseHeaderTimeout(time.Second * 10)
	if proxy != "" {
		client.SetProxyURL(proxy)
	}
	// Set common headers
	headers := map[string]string{
		"accept":                    "text/event-stream, text/event-stream",
		"accept-language":           "zh-CN,zh;q=0.9",
		"anthropic-client-platform": "web_claude_ai",
		"content-type":              "application/json",
		"origin":                    "https://claude.ai",
		"priority":                  "u=1, i",
	}
	for key, value := range headers {
		client.SetCommonHeader(key, value)
	}
	// Set cookies
	client.SetCommonCookies(&http.Cookie{
		Name:  "sessionKey",
		Value: sessionKey,
	})
	// Create default client with session key
	c := &Client{
		sessionKey: sessionKey,
		client:     client,
		defaultAttrs: map[string]interface{}{
			"personalized_styles": []map[string]interface{}{
				{
					"type":       "default",
					"key":        "Default",
					"name":       "Normal",
					"nameKey":    "normal_style_name",
					"prompt":     "Normal",
					"summary":    "Default responses from Claude",
					"summaryKey": "normal_style_summary",
					"isDefault":  true,
				},
			},
			"tools": []map[string]interface{}{
				{
					"type": "web_search_v0",
					"name": "web_search",
				},
			},
			"attachments":    []interface{}{},
			"files":          []interface{}{},
			"sync_sources":   []interface{}{},
			"rendering_mode": "messages",
			"timezone":       "America/New_York",
		},
	}
	return c
}

// SetOrgID sets the organization ID for the client
func (c *Client) SetOrgID(orgID string) {
	c.orgID = orgID
}
func (c *Client) GetOrgID() (string, error) {
	url := "https://claude.ai/api/organizations"
	resp, err := c.client.R().
		SetHeader("referer", "https://claude.ai/new").
		Get(url)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	type OrgResponse []struct {
		ID   int    `json:"id"`
		UUID string `json:"uuid"`
		Name string `json:"name"`
	}

	var orgs OrgResponse
	if err := json.Unmarshal(resp.Bytes(), &orgs); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	if len(orgs) == 0 {
		return "", errors.New("no organizations found")
	}
	return orgs[0].UUID, nil

}

// CreateConversation creates a new conversation and returns its UUID
func (c *Client) CreateConversation(model string) (string, error) {
	if c.orgID == "" {
		return "", errors.New("organization ID not set")
	}
	url := fmt.Sprintf("https://claude.ai/api/organizations/%s/chat_conversations", c.orgID)
	// 如果以-think结尾
	requestBody := map[string]interface{}{
		"model":                            model,
		"uuid":                             uuid.New().String(),
		"name":                             "",
		"include_conversation_preferences": true,
	}
	if len(model) > 6 && model[len(model)-6:] == "-think" {
		requestBody["paprika_mode"] = "extended"
		requestBody["model"] = model[:len(model)-6]
	}
	resp, err := c.client.R().
		SetHeader("referer", "https://claude.ai/new").
		SetBody(requestBody).
		Post(url)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	var result map[string]interface{}
	// logger.Info(fmt.Sprintf("create conversation response: %s", resp.String()))
	if err := json.Unmarshal(resp.Bytes(), &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	uuid, ok := result["uuid"].(string)
	if !ok {
		return "", errors.New("conversation UUID not found in response")
	}
	return uuid, nil
}

// SendMessage sends a message to a conversation and returns the status and response
func (c *Client) SendMessage(conversationID string, message string, stream bool, gc *gin.Context) (int, error) {
	if c.orgID == "" {
		return 500, errors.New("organization ID not set")
	}
	url := fmt.Sprintf("https://claude.ai/api/organizations/%s/chat_conversations/%s/completion",
		c.orgID, conversationID)
	// Create request body with default attributes
	requestBody := c.defaultAttrs
	requestBody["prompt"] = message
	requestBody["parent_message_uuid"] = "00000000-0000-4000-8000-000000000000"
	// Set up streaming response
	resp, err := c.client.R().DisableAutoReadResponse().
		SetHeader("referer", fmt.Sprintf("https://claude.ai/chat/%s", conversationID)).
		SetHeader("accept", "text/event-stream, text/event-stream").
		SetHeader("anthropic-client-platform", "web_claude_ai").
		SetHeader("cache-control", "no-cache").
		SetBody(requestBody).
		Post(url)
	if err != nil {
		return 500, fmt.Errorf("request failed: %w", err)
	}
	logger.Info(fmt.Sprintf("Claude response status code: %d", resp.StatusCode))
	if resp.StatusCode == http.StatusTooManyRequests {
		return http.StatusTooManyRequests, fmt.Errorf("rate limit exceeded")
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	return 200, c.HandleResponse(resp.Body, stream, gc)
}

// HandleResponse converts Claude's SSE format to OpenAI format and writes to the response writer
// HandleResponse converts Claude's SSE format to OpenAI format and writes to the response writer
func (c *Client) HandleResponse(body io.ReadCloser, stream bool, gc *gin.Context) error {
	defer body.Close()
	// Set headers for streaming
	if stream {
		gc.Writer.Header().Set("Content-Type", "text/event-stream")
		gc.Writer.Header().Set("Cache-Control", "no-cache")
		gc.Writer.Header().Set("Connection", "keep-alive")
		// 发送200状态码
		gc.Writer.WriteHeader(http.StatusOK)
		gc.Writer.Flush()
	} else {
		gc.Writer.Header().Set("Content-Type", "application/json")
		gc.Writer.Header().Set("Cache-Control", "no-cache")
		gc.Writer.Header().Set("Connection", "keep-alive")
	}
	scanner := bufio.NewScanner(body)
	// Keep track of the full response for the final message
	thinkingShown := false
	res_all_text := ""
	for scanner.Scan() {
		line := scanner.Text()
		// Skip empty lines
		if line == "" {
			continue
		}
		// logger.Info(fmt.Sprintf("Claude SSE line: %s", line))
		// Claude SSE lines start with "data: "
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		// Extract the data part
		data := line[6:]
		// logger.Info(fmt.Sprintf("Claude SSE data: %s", data))
		// Try to parse as ResponseEvent first
		var event ResponseEvent
		if err := json.Unmarshal([]byte(data), &event); err == nil {
			// Handle text_delta events
			if event.Type == "error" && event.Error.Message != "" {
				// Create OpenAI format response for error
				openAIResp := &OpenAISrteamResponse{
					ID:      uuid.New().String(),
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   "claude-3-7-sonnet-20250219",
					Choices: []StreamChoice{
						{
							Index: 0,
							Delta: Delta{
								Content: event.Error.Message,
							},
							Logprobs:     nil,
							FinishReason: nil,
						},
					},
				}
				jsonBytes, err := json.Marshal(openAIResp)
				// 加上data: 前缀
				jsonBytes = append([]byte("data: "), jsonBytes...)
				jsonBytes = append(jsonBytes, []byte("\n\n")...)
				if err != nil {
					logger.Error(fmt.Sprintf("Error marshalling JSON: %v", err))
					return err
				}

				// 发送数据
				gc.Writer.Write(jsonBytes)
				gc.Writer.Flush()
				return nil
			}
			if event.Delta.Type == "text_delta" && event.Delta.Text != "" {
				res_text := event.Delta.Text
				// Create OpenAI format response for text delta
				if thinkingShown {
					res_text = "</think>\n" + res_text
					thinkingShown = false
				}
				res_all_text += res_text
				if !stream {
					continue
				}
				openAIResp := &OpenAISrteamResponse{
					ID:      uuid.New().String(),
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   "claude-3-7-sonnet-20250219",
					Choices: []StreamChoice{
						{
							Index: 0,
							Delta: Delta{
								Content: res_text,
							},
							Logprobs:     nil,
							FinishReason: nil,
						},
					},
				}

				jsonBytes, err := json.Marshal(openAIResp)
				jsonBytes = append([]byte("data: "), jsonBytes...)
				jsonBytes = append(jsonBytes, []byte("\n\n")...)
				if err != nil {
					logger.Error(fmt.Sprintf("Error marshalling JSON: %v", err))
					return err
				}

				// 发送数据
				gc.Writer.Write(jsonBytes)
				gc.Writer.Flush()
				continue
			}
			// Handle thinking_delta events - only show once
			if event.Delta.Type == "thinking_delta" {
				res_text := event.Delta.THINKING
				if !thinkingShown {
					res_text = "<think>" + res_text
					thinkingShown = true
				}
				res_all_text += res_text
				if !stream {
					continue
				}
				// Create OpenAI format response for thinking notification
				openAIResp := &OpenAISrteamResponse{
					ID:      uuid.New().String(),
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   "claude-3-7-sonnet-20250219",
					Choices: []StreamChoice{
						{
							Index: 0,
							Delta: Delta{
								Content: res_text,
							},
							Logprobs:     nil,
							FinishReason: nil,
						},
					},
				}
				jsonBytes, err := json.Marshal(openAIResp)
				jsonBytes = append([]byte("data: "), jsonBytes...)
				jsonBytes = append(jsonBytes, []byte("\n\n")...)
				if err != nil {
					logger.Error(fmt.Sprintf("Error marshalling JSON: %v", err))
					return err
				}

				// 发送数据
				gc.Writer.Write(jsonBytes)
				gc.Writer.Flush()
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading response: %w", err)
	}
	if !stream {
		gc.Writer.Header().Set("Content-Type", "application/json")
		gc.Writer.Header().Set("Cache-Control", "no-cache")
		// Create final response with all text
		openAIResp := &OpenAIResponse{
			ID:      uuid.New().String(),
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   "claude-3-7-sonnet-20250219",
			Choices: []NoStreamChoice{
				{
					Index: 0,
					Message: Message{
						Role:       "assistant",
						Content:    res_all_text,
						refusal:    nil,
						annotation: []interface{}{},
					},
					Logprobs:     nil,
					FinishReason: "stop",
				},
			},
			Usage: Usage{
				PromptTokens:     0,
				CompletionTokens: len(res_all_text),
				TotalTokens:      len(res_all_text),
			},
		}
		jsonBytes, err := json.Marshal(openAIResp)
		if err != nil {
			logger.Error(fmt.Sprintf("Error NoStream marshalling JSON: %v", err))
		}
		gc.Writer.Write(jsonBytes)
		gc.Writer.Flush()
	} else {
		// 发送结束标志
		gc.Writer.Write([]byte("data: [DONE]\n\n"))
		gc.Writer.Flush()
	}

	return nil
}

// DeleteConversation deletes a conversation by ID
func (c *Client) DeleteConversation(conversationID string) error {
	if c.orgID == "" {
		return errors.New("organization ID not set")
	}
	url := fmt.Sprintf("https://claude.ai/api/organizations/%s/chat_conversations/%s",
		c.orgID, conversationID)
	requestBody := map[string]string{
		"uuid": conversationID,
	}
	resp, err := c.client.R().
		SetHeader("referer", fmt.Sprintf("https://claude.ai/chat/%s", conversationID)).
		SetBody(requestBody).
		Delete(url)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	return nil
}

// UploadFile uploads files to Claude and adds them to the client's default attributes
// fileData should be in the format: data:image/jpeg;base64,/9j/4AA...
func (c *Client) UploadFile(fileData []string) error {
	if c.orgID == "" {
		return errors.New("organization ID not set")
	}
	if len(fileData) == 0 {
		return errors.New("empty file data")
	}

	// Initialize files array in default attributes if it doesn't exist
	if _, ok := c.defaultAttrs["files"]; !ok {
		c.defaultAttrs["files"] = []interface{}{}
	}

	// Process each file
	for _, fd := range fileData {
		if fd == "" {
			continue // Skip empty entries
		}

		// Parse the base64 data
		parts := strings.SplitN(fd, ",", 2)
		if len(parts) != 2 {
			return errors.New("invalid file data format")
		}

		// Get the content type from the data URI
		metaParts := strings.SplitN(parts[0], ":", 2)
		if len(metaParts) != 2 {
			return errors.New("invalid content type in file data")
		}

		metaInfo := strings.SplitN(metaParts[1], ";", 2)
		if len(metaInfo) != 2 || metaInfo[1] != "base64" {
			return errors.New("invalid encoding in file data")
		}

		contentType := metaInfo[0]

		// Decode the base64 data
		fileBytes, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return fmt.Errorf("failed to decode base64 data: %w", err)
		}

		// Determine filename based on content type
		var filename string
		switch contentType {
		case "image/jpeg":
			filename = "image.jpg"
		case "image/png":
			filename = "image.png"
		case "application/pdf":
			filename = "document.pdf"
		default:
			filename = "file"
		}

		// Create the upload URL
		url := fmt.Sprintf("https://claude.ai/api/%s/upload", c.orgID)

		// Create a multipart form request
		resp, err := c.client.R().
			SetHeader("referer", "https://claude.ai/new").
			SetHeader("anthropic-client-platform", "web_claude_ai").
			SetFileBytes("file", filename, fileBytes).
			SetContentType("multipart/form-data").
			Post(url)

		if err != nil {
			return fmt.Errorf("request failed: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("unexpected status code: %d, response: %s", resp.StatusCode, resp.String())
		}

		// Parse the response
		var result struct {
			FileUUID string `json:"file_uuid"`
		}

		if err := json.Unmarshal(resp.Bytes(), &result); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}

		if result.FileUUID == "" {
			return errors.New("file UUID not found in response")
		}

		// Add file to default attributes
		c.defaultAttrs["files"] = append(c.defaultAttrs["files"].([]interface{}), result.FileUUID)
	}

	return nil
}

func (c *Client) SetBigContext(context string) {
	c.defaultAttrs["attachments"] = []map[string]interface{}{
		{
			"file_name":         "context.txt",
			"file_type":         "text/plain",
			"file_size":         len(context),
			"extracted_content": context,
		},
	}

}
