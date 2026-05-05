# Debug: LLM Request / Response

## 現状

`llm/client.go` の `Chat()` はプロバイダ毎の実装にディスパッチするが、リクエスト/レスポンスのトレース機構がない。

```go
func (c *Client) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResult, error) {
    switch normalizeProvider(c.Provider) {
    case "openai", ...: return c.chatOpenAICompatible(ctx, messages, tools)
    case "anthropic":    return c.chatAnthropic(ctx, messages, tools)
    ...
    }
}
```

**問題点**:
- 実際に送信されたプロンプトや受信した生レスポンスを確認できない
- トークン使用量が不明（コスト管理できない）
- レートリミットや API エラーのデバッグが ad-hoc
- 各プロバイダ実装に横断的なトレースを入れる仕組みがない

## 必要なトレースポイント

### リクエスト時

| フィールド | 説明 |
|-----------|------|
| `ts` | リクエスト送信時刻 |
| `provider` | openai / anthropic / gemini / ... |
| `model` | モデル名 |
| `msg_count` | メッセージ数 |
| `tool_count` | ツール定義数 |
| `system_prompt_len` | system メッセージのバイト数 |
| `total_input_len` | 全メッセージの合計バイト数（概算） |
| `max_tokens` | 指定 max_tokens |

### レスポンス時

| フィールド | 説明 |
|-----------|------|
| `ts` | レスポンス受信時刻 |
| `duration_ms` | リクエスト〜レスポンスの所要時間 |
| `status` | ok / error / timeout |
| `http_status` | HTTP ステータスコード |
| `content_len` | レスポンス本文のバイト数 |
| `tool_calls` | ツール呼び出し数 |
| `usage_prompt_tokens` | prompt token 数（API が返す場合） |
| `usage_completion_tokens` | completion token 数 |
| `error` | エラー時のみ |

## 実装方針

### ラッパーアプローチ

各プロバイダ実装（`chatOpenAICompatible`, `chatAnthropic`, `chatGemini`）を直接変更せず、`Chat()` 本体をラップ:

```go
func (c *Client) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResult, error) {
    start := time.Now()
    res, err := c.chat(ctx, messages, tools) // 既存の switch を内部関数に
    dur := time.Since(start)
    
    logLLM("provider=%s model=%s msg_count=%d tool_count=%d duration_ms=%d status=%s error=%v",
        c.Provider, c.Model, len(messages), len(tools), dur.Milliseconds(),
        statusStr(err), err)
    
    if res != nil {
        logLLM("content_len=%d tool_calls=%d", len(res.Content), len(res.ToolCalls))
    }
    
    return res, err
}
```

### 注意点

- メッセージ本文の全文ログは**デフォルトでは出力しない**（プライバシー / サイズ）
- `CLAWLET_DEBUG=llm,llm-body` で全文出力を明示的に許可
- 機密情報（API キーなど）は絶対にログに出さない
