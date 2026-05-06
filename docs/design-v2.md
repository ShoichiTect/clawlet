# clawlet v2 — 設計計画

## 1. 目的

- **プロジェクト単位の完全な状態分離**：workspace ごとにセッションを分離し、複数プロジェクト間で会話履歴が混ざらないようにする
- **外部チャンネル廃止**：Discord / Slack / Telegram / WhatsApp を削除し、独自 TUI からの内部接続のみに絞る
- **マルチプロジェクト Gateway**：同一マシンで複数 `clawlet gateway` を異なる `--dir` で起動し、TUI がプロジェクトを選択して接続できる

---

## 2. 現状の問題

### 2.1 セッションキーの衝突

CLI モードのデフォルトセッションキーが `cli:default` 固定のため、
`clawlet agent --workspace ./proj-a` と `clawlet agent --workspace ./proj-b` で
同じ `~/.clawlet/sessions/cli_default.jsonl` を読み書きして会話履歴が混ざる。

Gateway モードでも `channel:chatID` でキーを生成するが、
セッション保存先が `~/.clawlet/sessions/` に固定されているため、
別 `--workspace` で起動した複数 gateway 間でセッションが共有されてしまう。

### 2.2 外部チャンネルの複雑性

Discord / Slack / Telegram / WhatsApp の各インテグレーションがコードベースの大部分を占めているが、
今後の運用は自作 TUI 経由に一本化するため不要になる。
cron / heartbeat は workspace スコープで独立して動作しており、引き続き維持する。

---

## 3. アーキテクチャ

### 3.1 ディレクトリ構造

```
~/.clawlet/                          # グローバル状態（--dir 未指定時のデフォルト）
├── config.json                      # グローバル設定（--dir 時も共通で使用）
├── sessions/                        # --dir 未指定時のフォールバック
│   └── cli_default.jsonl
└── workspace/                       # --dir 未指定時のデフォルトworkspace
    └── memory/

my-project/                          # --dir で指定するプロジェクトルート
├── .clawlet/
│   ├── sessions/                    # このプロジェクト専用セッション
│   │   └── default.jsonl            # CLI も Gateway(TUI) も同じファイルを共有
│   └── gateway.sock                 # Unix domain socket（gateway 起動時）
├── memory/                          # 既存のまま（workspace 内）
│   ├── MEMORY.md
│   ├── HISTORY.md
│   └── 2026-05-06.md
├── AGENTS.md                        # 既存のまま（bootstrap file）
└── src/ ...
```

### 3.2 `--dir` フラグ

`--workspace` を廃止し `--dir` に統合。

```bash
# CLI モード（プロジェクト指定）
clawlet agent --dir ./my-project

# CLI モード（グローバルデフォルト）
clawlet agent

# Gateway モード（プロジェクト指定）
clawlet gateway --dir ./my-project

# Gateway モード（グローバルデフォルト）
clawlet gateway
```

| | `--dir` 指定あり | `--dir` なし（デフォルト） |
|---|---|---|
| workspace | `--dir` の絶対パス | `~/.clawlet/workspace` |
| sessions dir | `{dir}/.clawlet/sessions/` | `~/.clawlet/sessions/` |
| セッションキー(デフォルト) | `"default"`（CLI/Gateway 共通） | `"cli:default"`（従来通り） |
| memory | `{dir}/memory/` | `~/.clawlet/workspace/memory/` |

### 3.3 セッション分離

**設計思想**：`--dir` がコンテキスト境界。
同一ディレクトリなら CLI でも Gateway(TUI) でも同じセッションを共有する。

**`--dir` 指定時**：
- `sessionsDir = {dir}/.clawlet/sessions/`
- デフォルトキー = `"default"`（CLI / Gateway 共通）
- CLI で話した内容を TUI で引き継げる。その逆も可能。

**`--dir` 未指定時（グローバルモード）**：
- `sessionsDir = ~/.clawlet/sessions/`
- CLI のデフォルトキー = `"cli:default"`（従来通り）
- Gateway のデフォルトキー = `"gateway:default"`

**`--session` / `-s` フラグ**は引き続き明示指定を優先：
```bash
clawlet agent --dir ./proj -s my-custom-key   # key = "my-custom-key"
```

### 3.4 Gateway — Unix Domain Socket

外部チャンネルを廃止し、TUI からの内部接続のみを受け付ける。

```go
// {dir}/.clawlet/gateway.sock で listen
listener, _ := net.Listen("unix", filepath.Join(dir, ".clawlet", "gateway.sock"))
```

#### HTTP over Unix Socket のエンドポイント

```
POST /api/chat
  Content-Type: application/json
  Body: {"message": "...", "session_key": "default"}

  Response: 200
  {"content": "...", "tools_used": ["read_file", "exec"]}

GET /api/health
  Response: 200
  {"status": "ok", "workspace": "/Users/vps/dev/proj-a", "pid": 12345}
```

`session_key` は省略可（デフォルト `"default"`）。
同一ディレクトリの CLI モードと同じセッションファイル `default.jsonl` に保存される。

TUI は既知のプロジェクトディレクトリ群を走査し、`{dir}/.clawlet/gateway.sock` の存在を確認して接続する。

### 3.5 ツール構成の変更

| ツール | 変更 |
|---|---|
| `read_file`, `write_file`, `edit_file`, `list_dir` | 変更なし |
| `exec` | 変更なし |
| `web_fetch`, `web_search` | 変更なし |
| `read_skill`, `find_skills`, `install_skill` | 変更なし |
| `memory_search`, `memory_get` | 変更なし |
| `message` | **削除**（外部チャンネル廃止に伴い） |
| `spawn` | **削除**（subagent は TUI 側の責務） |

### 3.6 削除するパッケージ

| パッケージ | 理由 |
|---|---|
| `channels/` 全体 | 外部チャンネル廃止（Discord/Slack/Telegram/WhatsApp） |
| `bus/` | 内部通信にバスが不要（Unix socket HTTP に一本化） |

### 3.7 残すパッケージ

| パッケージ | 用途 |
|---|---|
| `agent/` | Prompt, Turn, Consolidation, Agent (CLI), Loop (Gateway) |
| `config/` | 設定読み込み（チャンネル設定部分は削除） |
| `cron/` | スケジュール実行（workspace スコープ、維持） |
| `heartbeat/` | 定期ハートビート（workspace スコープ、維持） |
| `llm/` | LLM クライアント |
| `memory/` | 長期記憶、検索インデックス |
| `paths/` | グローバルパス解決（ConfigDir, ConfigPath 等） |
| `session/` | セッション CRUD |
| `skills/` | スキルローダー |
| `tools/` | ツール定義・実行（message/spawn を除く） |
| `media/` | 添付ファイル処理 |
| `cmd/clawlet/` | CLI エントリポイント |

---

## 4. 実装計画

### Phase 1：コア再設計（今回）

#### Step 1.1：`--dir` フラグ実装

- `cmd/clawlet/cmd_agent.go`：`--dir` 追加、`--workspace` 削除
- `cmd/clawlet/cmd_gateway.go`：同上
- `cmd/clawlet/config.go`：`resolveWorkspace` → `resolveDir` に変更。
  - `resolveDir(dirFlag string) (wsAbs string, sessionsDir string, err error)`
  - `--dir` あり → wsAbs = dir, sessionsDir = dir/.clawlet/sessions
  - `--dir` なし → wsAbs = ~/.clawlet/workspace, sessionsDir = ~/.clawlet/sessions

#### Step 1.2：セッション分離

- `agent/agent.go`：`Options` に `SessionDir` 追加。空なら `paths.SessionsDir()`。
- `agent/agent.go`：`Process()` で `session.Save(sessionDir, sess)`
- `--dir` 指定時：デフォルトセッションキー = `"default"`（CLI/Gateway 共通、同一ファイル共有）
- `--dir` 未指定時：CLI は `"cli:default"`、Gateway は `"gateway:default"`（従来通り）

#### Step 1.3：外部チャンネル削除

- `channels/` ディレクトリ削除
- `bus/` ディレクトリ削除
- `cmd/clawlet/cmd_channels.go` 削除
- `config/config.go` から ChannelsConfig 関連を削除
- `tools/registry.go` から Outbound 関連を削除（Spawn も削除）
- `tools/tool_message.go`, `tools/tool_spawn.go` 削除
- `tools/defs.go` から message/spawn ツール定義を削除
- `agent/loop.go` を Gateway 内部通信用に再設計（bus を使わず直接 HTTP ハンドラを登録）
- `agent/subagent.go` 削除
- `cron/`, `heartbeat/` は**維持**。channel 非依存のためそのまま使える

#### Step 1.4：Gateway Unix Socket

- `cmd/clawlet/cmd_gateway.go`：Unix socket で listen する HTTP サーバーを起動
- エンドポイント：`POST /api/chat`、`GET /api/health`
- socket ファイルの cleanup（終了時に削除）

#### Step 1.5：設定の簡略化

- `config/config.go`：ChannelsConfig 関連フィールドを削除
- CronConfig, HeartbeatConfig は**維持**
- 残す項目：`env`, `agents`, `llm`, `tools`（exec, web, skills, media）

#### Step 1.6：テスト修正

- 削除に伴って壊れるテストを修正・削除
- `go test ./...` が通る状態に

### Phase 2：TUI 連携（将来）

- TUI 側で gateway socket のディスカバリ実装
- マルチプロジェクトのセッション管理 UI
- ツール実行のリアルタイム表示

---

## 5. 削除対象一覧

### ファイル（削除）

```
channels/channels.go
channels/manager.go
channels/manager_test.go
channels/discord/
channels/slack/
channels/telegram/
channels/whatsapp/
bus/bus.go
tools/tool_message.go
tools/tool_message_test.go
tools/tool_spawn.go
agent/subagent.go
cmd/clawlet/cmd_channels.go
cmd/clawlet/cmd_gateway_security_test.go
```

### ファイル（編集）

```
cmd/clawlet/main.go                    # channels コマンド登録削除
cmd/clawlet/cmd_agent.go               # --dir 追加、--workspace 削除、セッションキー変更
cmd/clawlet/cmd_gateway.go             # --dir 追加、--workspace 削除、Unix socket 化
cmd/clawlet/cmd_cron.go                # channel 参照除去
cmd/clawlet/cmd_status.go              # チャンネル参照削除
cmd/clawlet/config.go                  # resolveDir 追加、env overrides 簡略化
config/config.go                       # ChannelsConfig 設定削除（cron/heartbeat は維持）
config/config_test.go                  # 同上
tools/registry.go                      # Outbound/Spawn フィールド削除、Definitions/Execute 簡略化
tools/defs.go                          # message/spawn 定義削除
agent/agent.go                         # SessionDir 追加
agent/loop.go                          # 再設計（bus 除去、HTTP ハンドラ登録）
agent/prompt.go                        # gateway 用プロンプト簡略化
agent/turn.go                          # 変更なし
agent/consolidation.go                 # 変更なし
agent/consolidation_test.go            # 変更なし
session/session.go                     # 変更なし
memory/memory.go                       # 変更なし
memory/index_manager.go                # 変更なし
memory/index_manager_test.go           # 変更なし
paths/paths.go                         # 変更なし
```

---

## 6. 設定ファイル (config.json) の新構造

```jsonc
{
  "env": {},
  "agents": {
    "defaults": {
      "model": "openrouter/openai/gpt-4o-mini",
      "memoryWindow": 50,
      "memorySearch": { /* ... 変更なし */ }
    }
  },
  "llm": {
    "provider": "",
    "apiKey": "",
    "baseURL": "",
    "model": "",
    "headers": {}
  },
  "tools": {
    "restrictToWorkspace": true,
    "exec": { "timeoutSec": 60 },
    "web": {
      "braveApiKey": "",
      "allowedDomains": ["*"],
      "blockedDomains": [],
      "maxResponseBytes": 500000,
      "fetchTimeoutSec": 30
    },
    "skills": { /* ... 変更なし */ },
    "media": { /* ... 変更なし */ }
  }
  "cron": { "storePath": ".clawlet/cron.json" },
  "heartbeat": { "enabled": false, "intervalSec": 1800 }
  // channels, gateway, bus フィールド → 削除
}
```

---

## 7. 移行上の注意

- **破壊的変更**：外部チャンネルが完全に削除されるため、Discord/Slack/Telegram/WhatsApp 連携は使えなくなる
- **セッション**：`~/.clawlet/sessions/cli_default.jsonl` に蓄積された履歴は、
  `--dir` なしモードではそのまま使えるが、`--dir` 指定時は新しいパスに新規セッションが作られる（移行なし）
- **config.json**：削除されたフィールドは `Load()` で単に無視される（JSON unmarshal の挙動による）

---

## 8. 実装進捗（2026-05-06）

### Phase 1：コア再設計 — 実装済み

#### 完了項目

- `--workspace` を廃止し、`--dir` に統合
  - `agent`, `gateway`, `onboard`, `cron` に `--dir` を導入
  - `resolveWorkspace` を `resolveDir` に置き換え
- セッション分離を実装
  - `--dir` 指定時: `{dir}/.clawlet/sessions/`
  - `--dir` 未指定時: `~/.clawlet/sessions/`
  - `--dir` 指定時のデフォルトセッションキーは `default`
  - `--dir` 未指定時のデフォルトセッションキーは CLI が `cli:default`、Gateway が `gateway:default`
  - `agent.Options` に `SessionDir` を追加
- Gateway を Unix domain socket HTTP に変更
  - socket path: `{workspace}/.clawlet/gateway.sock`
  - `GET /api/health` 実装
  - `POST /api/chat` 実装
  - TCP listen / public bind policy を削除
- 外部チャンネルを削除
  - `channels/` 全体を削除
  - `bus/` を削除
  - `cmd/clawlet/cmd_channels.go` を削除
  - Discord / Slack / Telegram / WhatsApp 関連テストも削除
- `message` / `spawn` tool を削除
  - `tools/tool_message.go` を削除
  - `tools/tool_spawn.go` を削除
  - `agent/subagent.go` を削除
  - tool 定義・registry から `message` / `spawn` を除去
- `agent/loop.go` を bus 非依存に再設計
  - HTTP handler から同期的に `ProcessTurn` / `ProcessDirect` を呼ぶ構成に変更
- config を簡略化
  - `gateway` / `channels` config を削除
  - `cron.storePath` を追加
  - 旧 config の削除済みフィールドは JSON unmarshal により無視される
- cron を workspace / session-key ベースに変更
  - channel delivery (`channel`, `to`, `deliver`) を廃止
  - payload に `session_key` を追加
  - store path はデフォルトで `{workspace}/.clawlet/cron.json`
- media package を bus 非依存に変更
  - `media.Attachment` / `media.InboundMessage` / `media.InferAttachmentKind` を定義
- README を v2 仕様に更新
- 不要依存を `go mod tidy` で削除

#### 確認済み

```bash
go test ./...
go vet ./...
go install ./cmd/clawlet
```

すべて成功。

#### 実装上の補足

- `GET /api/health` の response は設計通り `status`, `workspace`, `pid` を返す。
- `POST /api/chat` の response は `content`, `tools_used` を返す。
- `session_key` 省略時のデフォルトは、`--dir` 指定ありなら `default`、未指定なら `gateway:default`。
- heartbeat は維持し、Gateway loop の `heartbeat` session を使って実行する。
- cron CLI は外部チャンネル送信を行わず、Gateway 内部の agent turn として実行する。

### Phase 2：TUI 連携 — 未着手

- TUI 側の socket discovery
- マルチプロジェクト UI
- ツール実行のリアルタイム表示
