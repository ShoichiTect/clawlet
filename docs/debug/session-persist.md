# Debug: Session Persistence

## 現状

`session/session.go` の `Save()` は `agent/agent.go` と `agent/loop.go` の両方で呼ばれているが、エラーが無視されている。

```go
// agent/agent.go
a.sess.Add("user", input)
a.sess.AddWithTools("assistant", final, toolsUsed)
_ = session.Save(a.sessionDir, a.sess) // ← エラー無視
```

```go
// agent/loop.go
sess.Add("user", sessionUserText)
sess.AddWithTools("assistant", final, toolsUsed)
_ = l.sessions.Save(sess) // ← エラー無視
```

**問題点**:
- ディスクフル、パーミッションエラー、JSONL 破損などがサイレントに発生
- ユーザーは会話が保存されたと思っているが実際は保存されていない可能性がある
- consolidation 失敗も stderr 出力のみで、gateway モードでは確認不能

## 必要なトレースポイント

### Save 時

| フィールド | 説明 |
|-----------|------|
| `ts` | 時刻 |
| `session_key` | セッションキー |
| `msg_count` | 保存時のメッセージ数 |
| `file_size` | 保存後のファイルサイズ（byte） |
| `status` | ok / error |
| `error` | エラーメッセージ（error 時のみ） |

### Consolidation 時

| フィールド | 説明 |
|-----------|------|
| `ts` | 時刻 |
| `session_key` | セッションキー |
| `before_count` | consolidation 前のメッセージ数 |
| `after_count` | consolidation 後のメッセージ数 |
| `status` | ok / skipped / error |
| `error` | エラーメッセージ（error 時のみ） |

### Load 時

| フィールド | 説明 |
|-----------|------|
| `ts` | 時刻 |
| `session_key` | セッションキー |
| `msg_count` | ロードしたメッセージ数 |
| `file_size` | ファイルサイズ（byte） |
| `status` | ok / new / error |

## 実装方針

```go
// session/debug.go (新規)

func debugSessionLog(format string, args ...any) {
    if !debugEnabled("session") {
        return
    }
    writeDebugLog("session", format, args...)
}

func Save(dir string, s *Session) error {
    err := saveInternal(dir, s)
    debugSessionLog("ts=%s session_key=%s msg_count=%d status=%s error=%v",
        time.Now().Format(time.RFC3339Nano), s.Key, len(s.Messages),
        status(err), err)
    return err
}
```

### 呼び出し元の修正

```go
// agent/agent.go
if err := session.Save(a.sessionDir, a.sess); err != nil {
    // debug ログは Session.Save 内で出力済み
    // CLI モードではユーザーに警告
    if a.verbose {
        fmt.Fprintf(os.Stderr, "warning: session save failed: %v\n", err)
    }
}
```

## 参照

- `session/session.go` — `Save()`, `Load()`, `Manager`
- `agent/agent.go` — CLI パスの save 呼び出し
- `agent/loop.go` — gateway パスの save 呼び出し
- `agent/consolidation.go` — consolidation ロジック
