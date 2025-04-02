package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"bufio"

	"github.com/WuKongIM/go-pdk/pdk"
	"github.com/WuKongIM/wklog"
	"go.uber.org/zap"
)

var PluginNo = "wk.plugin.ai-openrouter" // 插件编号
var Version = "0.0.1"                    // 插件版本
var Priority = int32(1)                  // 插件优先级

func main() {
	err := pdk.RunServer(New, PluginNo, pdk.WithVersion(Version), pdk.WithPriority(Priority))
	if err != nil {
		panic(err)
	}
}

type Config struct {
	ApiKey pdk.SecretKey `json:"api_key" label:"OpenRouter API Key"`
	Model  string        `json:"model" label:"AI Model"`
}

type Robot struct {
	wklog.Log
	Config Config // 插件的配置，名字必须为Config, 声明了以后，可以在WuKongIM后台配置
}

func New() interface{} {
	return &Robot{
		Log: wklog.NewWKLog("robot"),
		Config: Config{
			Model: "deepseek/deepseek-chat-v3-0324:free", // 默认模型
		},
	}
}

func (r *Robot) ConfigUpdate() {
	fmt.Println("config update...", r.Config.ApiKey)
}

// OpenRouter API 请求结构
type OpenRouterRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type Message struct {
	Role    string      `json:"role"`
	Content string      `json:"content"`
	Refusal interface{} `json:"refusal"`
}

// OpenRouter API 响应结构
type OpenRouterResponse struct {
	ID       string   `json:"id"`
	Provider string   `json:"provider"`
	Model    string   `json:"model"`
	Object   string   `json:"object"`
	Created  int      `json:"created"`
	Choices  []Choice `json:"choices"`
	Usage    Usage    `json:"usage"`
}

type Choice struct {
	LogProbs           interface{} `json:"logprobs"`
	FinishReason       string      `json:"finish_reason"`
	NativeFinishReason string      `json:"native_finish_reason"`
	Index              int         `json:"index"`
	Message            Message     `json:"message"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// 实现插件的回复消息方法
func (r *Robot) Receive(c *pdk.Context) {
	var payload map[string]interface{}
	err := json.Unmarshal(c.RecvPacket.Payload, &payload)
	if err != nil {
		r.Error("unmarshal payload error:", zap.Error(err))
		return
	}

	var content string
	if payload["content"] != nil {
		content = payload["content"].(string)
	}

	// 创建 OpenRouter 请求
	reqBody := OpenRouterRequest{
		Model: r.Config.Model,
		Messages: []Message{
			{
				Role:    "user",
				Content: content,
			},
		},
		Stream: true,
	}

	// 将请求体转换为 JSON
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		r.Error("marshal request error:", zap.Error(err))
		return
	}

	// 打开流用于逐步响应
	imstream, err := c.OpenStream(pdk.StreamWithPayload(&pdk.PayloadText{
		Content: "正在思考中...",
		Type:    1,
	}))
	if err != nil {
		r.Error("open stream error:", zap.Error(err))
		return
	}
	defer imstream.Close()

	// 创建 HTTP 请求
	req, err := http.NewRequest("POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		r.Error("create request error:", zap.Error(err))
		return
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.Config.ApiKey.String())
	req.Header.Set("User-Agent", "WuKongIM/1.0.0")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Host", "openrouter.ai")
	req.Header.Set("Connection", "keep-alive")

	// 发送请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		r.Error("send request error:", zap.Error(err))
		return
	}
	defer resp.Body.Close()

	// 处理流式响应
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		r.Error("API error:", zap.String("status", resp.Status), zap.String("body", string(body)))
		return
	}

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			r.Error("read stream error:", zap.Error(err))
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var streamResponse struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}

		if err := json.Unmarshal([]byte(data), &streamResponse); err != nil {
			r.Error("unmarshal stream data error:", zap.Error(err), zap.String("data", data))
			continue
		}

		if len(streamResponse.Choices) > 0 {
			content := streamResponse.Choices[0].Delta.Content
			if content != "" {
				fmt.Print(content)
				streamData, _ := json.Marshal(map[string]interface{}{
					"type":    1,
					"content": content,
				})
				imstream.Write(streamData)
			}
		}
	}
}
