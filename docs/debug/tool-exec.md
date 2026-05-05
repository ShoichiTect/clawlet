# Debug: Tool Execution

## 現状

`tools/registry.go` の `Execute()` は巨大な switch 文で、各ツールの呼び出し前後に観測ポイントがない。

```go
// agent/agent.go の CLI パス（手動 observer）
observer := func(ev agent.ToolEvent) {
    fmt.Println(toolStyle.Sprintf("--- tool --- %s %s", ev.Name, ev.Args))
    ...
}

// agent/loop.go の gateway パス（observer なし）
messages = appendToolRound(..., func(tc llm.ToolCall) string {
    out, err := l.tools.Execute(ctx, tctx, tc.Name, tc.Arguments)
    ...
})
```

**問題点**:
- CLI は ad-hoc な `fmt.Printf` 頼りで構造化されておらず、テスト不能
- gateway パス（loop.go）には tool observer すら渡せない
- ツール実行の成功/失敗、所要時間、出力サイズなどを横断的に取得する仕組みがない

## 必要なトレースポイント

各ツール実行で以下を記録:

| フィールド | 型 | 説明 |
|-----------|-----|------|
| `ts` | RFC3339Nano | 実行開始時刻 |
| `tool` | string | ツール名（read_file, exec, ...） |
| `args` | string | 引数プレビュー（先頭200文字） |
| `duration_ms` | int | 実行時間（ミリ秒） |
| `status` | ok/error/timeout | 実行結果 |
| `output_len` | int | 出力のバイト数 |
| `output_preview` | string | 出力プレビュー（先頭200文字） |
| `error` | string | エラー時のみ |

## 実装方針

### Phase 1: Execute のラッパー

`tools/registry.go` の `Execute` をラップする debug レイヤーを追加:

```go
// tools/debug.go (新規)

package tools

import (
    "context"
    "encoding/json"
    "fmt"
    "os"
    "time"
)

type debugLogger func(format string, args ...any)

func debugToolLogger() debugLogger {
    if os.Getenv("CLAWLET_DEBUG") == "" {
        return nil
    }
    // category=tool の場合のみ有効
    ...
}

func (r *Registry) ExecuteWithDebug(ctx context.Context, tctx Context, name string, args json.RawMessage) (string, error) {
    log := debugToolLogger()
    if log == nil {
        return r.Execute(ctx, tctx, name, args)
    }
    start := time.Now()
    out, err := r.Execute(ctx, tctx, name, args)
    dur := time.Since(start)
    status := "ok"
    errStr := ""
    if err != nil {
        status = "error"
        errStr = err.Error()
    }
    log("tool=%s args=%s duration_ms=%d status=%s output_len=%d error=%s",
        name, preview(args, 200), dur.Milliseconds(), status, len(out), errStr)
    return out, err
}
```

### Phase 2: structured event への移行（将来的）

Priority 2 の event bus が実装されたら、デバッグログ出力を event subscriber に置き換える。

## テスト

```go
func TestExecuteWithDebug_LogsOnSuccess(t *testing.T) { ... }
func TestExecuteWithDebug_LogsOnError(t *testing.T) { ... }
func TestExecuteWithDebug_SkipsWhenDisabled(t *testing.T) { ... }
```
