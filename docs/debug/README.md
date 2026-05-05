# Debug

clawlet の debug / 可観測性（observability）に関する issue と実装方針をまとめる。

## 共通方針

- 環境変数 `CLAWLET_DEBUG` でカテゴリを指定（カンマ区切り）:
  ```bash
  CLAWLET_DEBUG=input,tool,session clawlet agent
  ```
- 各カテゴリは個別のログファイルに出力: `$TMPDIR/clawlet-debug-<category>.log`
- `CLAWLET_DEBUG=all` ですべて有効
- 本番（gateway モードなど）ではデフォルト無効、明示的に指定されたときのみ出力

### ログフォーマット（共通）

```
2026-05-06T12:34:56.789Z [category] event_name key=value key=value
```

JSON Lines ではなく人間可読な key=value 形式。`grep` や `awk` で扱いやすくするため。

## カテゴリ一覧

| カテゴリ | ファイル | 内容 | ステータス |
|----------|----------|------|------------|
| `input` | [cli-input.md](./cli-input.md) | raw モード入力のバイトトレース、状態遷移 | 🔴 issue 検出済・未着手 |
| `tool` | [tool-exec.md](./tool-exec.md) | ツール実行の引数/出力/時間/エラー | ⚫ 未着手 |
| `session` | [session-persist.md](./session-persist.md) | セッション保存の成功/失敗、consolidation | ⚫ 未着手 |
| `llm` | [llm-trace.md](./llm-trace.md) | LLM リクエスト/レスポンス、トークン使用量 | ⚫ 未着手 |
| `turn` | [turn-exec.md](./turn-exec.md) | turn 実行のライフサイクル、イベント順序 | ⚫ 未着手 |

## 参照

- 全体 TODO: [../todo.md](../todo.md)
- Priority 2: Structured Events And Event Bus
- Priority 3: Tool Execution Observable
- Priority 4: Session Persistence And Failure Visibility
