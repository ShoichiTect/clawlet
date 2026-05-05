# Debug: Turn Execution

## 現状

turn（ユーザー入力 → ツール呼び出しループ → 最終応答）の実行パスが `agent/agent.go`（CLI）と `agent/loop.go`（gateway）で重複しており、それぞれ観測方法が異なる。

```go
// CLI: agent/agent.go Process()
for iter := 0; iter < a.maxIters; iter++ {
    res, err := a.llm.Chat(ctx, messages, toolsDefs)
    ...
}

// Gateway: agent/loop.go processDirect()
for iter := 0; iter < l.maxIters; iter++ {
    res, err := l.llm.Chat(ctx, messages, toolsDefs)
    ...
}
```

**問題点**:
- turn の開始/終了、イテレーション回数、中断理由を横断的に把握できない
- 複数 turn にまたがるパフォーマンス分析が不可能
- consolidation が発火したタイミングと結果が追跡不能

## 必要なトレースポイント

### Turn 開始

| フィールド | 説明 |
|-----------|------|
| `ts` | turn 開始時刻 |
| `turn_id` | 連番 or UUID |
| `session_key` | セッションキー |
| `user_input_len` | ユーザー入力のバイト数 |
| `history_count` | 履歴メッセージ数 |

### 各イテレーション

| フィールド | 説明 |
|-----------|------|
| `turn_id` | 紐づく turn |
| `iter` | イテレーション番号 (0, 1, 2, ...) |
| `has_tool_calls` | ツール呼び出しの有無 |
| `tool_count` | ツール呼び出し数 |
| `tool_names` | 呼び出されたツール名（カンマ区切り） |

### Turn 終了

| フィールド | 説明 |
|-----------|------|
| `turn_id` | 紐づく turn |
| `total_iters` | 総イテレーション数 |
| `duration_ms` | turn 全体の所要時間 |
| `status` | completed / max_iters / error |
| `output_len` | 最終応答のバイト数 |
| `error` | エラー時のみ |

## 実装方針

### Phase 1: 共通 turn runner の抽出（Priority 1 と連動）

```go
// agent/turn.go (新規、または既存の shared runner)

type TurnTrace struct {
    ID        string
    Iterations []IterTrace
    Start     time.Time
    End       time.Time
    Status    string
    Error     error
}

type IterTrace struct {
    Index      int
    ToolNames  []string
    ToolCount  int
}
```

### Phase 2: debug ログ出力

共通 turn runner が `CLAWLET_DEBUG=turn` 時に各イベントをログ出力する。

## 参照

- `docs/todo.md` Priority 1: Unify The Turn Execution Path
- `agent/agent.go` — CLI turn 実行
- `agent/loop.go` — gateway turn 実行
- `agent/consolidation.go` — consolidation ロジック
