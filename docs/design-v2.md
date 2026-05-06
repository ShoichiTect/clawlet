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

### Phase 2：TUI 連携 — 要件定義中

- TUI 側の socket discovery
- マルチプロジェクト UI
- Gateway 経由の chat UI
- ツール実行のリアルタイム表示（MVP 後）

---

## 9. Phase 2：TUI 要件定義

### 9.1 方針

clawlet v2 の TUI は、外部チャンネル Discord / Slack / Telegram / WhatsApp の代替として、ローカルマシン上の複数プロジェクトに対して安全に接続し、会話・セッション・Gateway 状態を操作できる UI とする。

TUI は Gateway と Unix domain socket HTTP で通信する。TCP port は開けず、既存の v2 Gateway API を利用する。

### 9.2 採用方針

| 項目 | 方針 |
|---|---|
| 起動コマンド | `clawlet tui` |
| Gateway 起動 | MVP では自動起動しない。ユーザーが `clawlet gateway --dir <project>` を手動起動する |
| Project list 保存先 | `~/.clawlet/tui/projects.json` |
| TUI ライブラリ | Bubble Tea |
| package 構成 | `cmd/clawlet/cmd_tui.go` + `tui/` |
| 依存追加 | Bubble Tea は Phase 2 実装開始時に追加する |
| MVP 到達点 | project 一覧 → Gateway online project 選択 → message 送信 → assistant response 表示 |

### 9.3 MVP スコープ

#### 必須機能

- `clawlet tui` コマンドで起動する
- Project 一覧を表示する
- Project ごとに `{dir}/.clawlet/gateway.sock` を検出する
- `GET /api/health` で Gateway の online / offline を判定する
- Gateway online な project を選択して chat 画面に遷移する
- `POST /api/chat` で message を送信する
- assistant response の `content` を表示する
- response の `tools_used` を表示する
- session key を指定できる
- Gateway offline / stale socket / timeout / API error を分かりやすく表示する

#### MVP では扱わないもの

- Gateway の自動起動
- 複数 Gateway への同時接続
- ツール実行のリアルタイムストリーミング
- セッション履歴の高度な検索
- cron / heartbeat の管理 UI
- config.json の編集 UI

### 9.4 画面要件

#### Project List 画面

表示項目：

- project path
- Gateway status
  - `online`
  - `offline`
  - `stale socket`
  - `error`
- Gateway pid（online 時）
- workspace path（health response 由来）
- active session key

操作：

| 操作 | 内容 |
|---|---|
| `Enter` | 選択中 project に接続 |
| `r` | 再スキャン |
| `a` | project path を手入力して追加 |
| `d` | project list から削除 |
| `q` | 終了 |

Gateway offline の project を選択した場合は、起動コマンドを表示する。

```bash
clawlet gateway --dir /path/to/project
```

#### Chat 画面

表示項目：

- project path
- session key
- Gateway status / pid
- 会話ログ
- message 入力欄
- latest response の `tools_used`

操作：

| 操作 | 内容 |
|---|---|
| `Enter` | message 送信 |
| `Esc` | Project List に戻る |
| `Ctrl+S` | session key を変更 |
| `Ctrl+R` | health を再取得 |
| `Ctrl+L` | 画面クリア |
| `Ctrl+C` | 終了 |

### 9.5 Project discovery

TUI は以下の候補から project を発見する。

1. `~/.clawlet/tui/projects.json` に保存された project path
2. 現在の working directory
3. `~/.clawlet/workspace`
4. ユーザーが手入力で追加した path

Gateway socket path：

```text
{workspace}/.clawlet/gateway.sock
```

Health check：

```http
GET /api/health
```

Response：

```json
{
  "status": "ok",
  "workspace": "/Users/vps/dev/proj-a",
  "pid": 12345
}
```

### 9.6 TUI state

Project list は TUI 専用 state として保存する。

```text
~/.clawlet/tui/projects.json
```

初期構造：

```json
{
  "projects": [
    {
      "path": "/Users/vps/dev/proj-a",
      "session_key": "default",
      "last_opened_at": "2026-05-06T00:00:00Z"
    }
  ]
}
```

- `path` は絶対パスで保存する
- `session_key` は project ごとの最後に使った session key
- `last_opened_at` は並び替えや最近使った project 表示に使う

### 9.7 Chat API

既存 Gateway API を使用する。

```http
POST /api/chat
Content-Type: application/json
```

Request：

```json
{
  "message": "hello",
  "session_key": "default"
}
```

Response：

```json
{
  "content": "...",
  "tools_used": ["read_file", "exec"]
}
```

TUI は原則として `session_key` を明示的に送る。

### 9.8 非機能要件

- Unix domain socket のみを使う
- TCP port は開けない
- health check には短い timeout を設定する
- chat request 中は loading 表示を出す
- offline / stale socket / timeout を UI 上で区別する
- stale socket は削除せず、ユーザーに状態として表示する
- Windows 対応は MVP 範囲外とする

### 9.9 将来拡張

#### Streaming / リアルタイム表示

ツール実行や assistant delta をリアルタイム表示する場合は、Gateway API の拡張が必要。

候補：

```http
POST /api/chat/stream
```

イベント例：

```json
{"type":"assistant_delta","content":"..."}
{"type":"tool_start","name":"read_file","input":{}}
{"type":"tool_end","name":"read_file","output_summary":"..."}
{"type":"done","content":"..."}
```

#### Gateway 自動起動

MVP 後に、offline project に対して TUI から Gateway を起動できるようにする余地を残す。

```bash
clawlet gateway --dir <project>
```

### 9.10 実装計画

Phase 2 MVP は以下の順序で実装する。

#### Step 2.1：`clawlet tui` コマンド追加

- `cmd/clawlet/cmd_tui.go` を追加する
- `cmd/clawlet/main.go` に `tui` command を登録する
- 実処理は `tui/` package に委譲する

想定構成：

```text
cmd/clawlet/cmd_tui.go

tui/
├── app.go              # Bubble Tea program 起動
├── state.go            # ~/.clawlet/tui/projects.json 読み書き
├── client.go           # Unix socket HTTP client
├── project.go          # project discovery / health model
├── model_project.go    # Project List 画面
└── model_chat.go       # Chat 画面
```

#### Step 2.2：Bubble Tea 依存追加

Phase 2 実装開始時に Bubble Tea を追加する。

```bash
go get github.com/charmbracelet/bubbletea@latest
```

必要に応じて、入力欄や viewport のために Charmbracelet 系 package を追加する。

候補：

```bash
go get github.com/charmbracelet/bubbles@latest
go get github.com/charmbracelet/lipgloss@latest
```

依存追加後は以下を確認する。

```bash
go mod tidy
go test ./...
```

#### Step 2.3：TUI state 読み書き

- `~/.clawlet/tui/projects.json` を読み書きする
- ファイルが存在しない場合は空 state として扱う
- project path は絶対パスに正規化して保存する
- `session_key` 未設定時は `default` を使う
- `last_opened_at` を更新する

#### Step 2.4：Unix socket client 実装

- `{workspace}/.clawlet/gateway.sock` に対する HTTP client を実装する
- `GET /api/health` を呼び出す
- `POST /api/chat` を呼び出す
- timeout を設定する
- offline / stale socket / timeout / API error を区別して返す

#### Step 2.5：Project List model 実装

- state / cwd / `~/.clawlet/workspace` から project 候補を集める
- health check により Gateway status を表示する
- `Enter` で online project の Chat 画面へ遷移する
- `r` で再スキャンする
- `a` で project を追加する
- `d` で project を削除する
- `q` で終了する

#### Step 2.6：Chat model 実装

- message 入力欄を表示する
- `Enter` で `/api/chat` に送信する
- assistant response の `content` を会話ログに追加する
- `tools_used` を latest tools として表示する
- `Esc` で Project List に戻る
- `Ctrl+S` で session key を変更する
- `Ctrl+R` で health を再取得する
- `Ctrl+L` で画面をクリアする

#### Step 2.7：検証

以下を成功条件とする。

```bash
go test ./...
go vet ./...
go install ./cmd/clawlet
```

手動確認：

```bash
clawlet gateway --dir /path/to/project
clawlet tui
```

期待動作：

1. Project List に `/path/to/project` が online として表示される
2. Project を選択して Chat 画面に遷移できる
3. message を送信できる
4. assistant response と tools_used が表示される
5. TUI を再起動しても `~/.clawlet/tui/projects.json` から project が復元される

### 9.11 実装進捗（2026-05-06）

#### Phase 2 MVP — 初期実装済み

完了項目：

- `clawlet tui` コマンドを追加
  - `cmd/clawlet/cmd_tui.go`
  - `cmd/clawlet/main.go` に command 登録
- Bubble Tea ベースの TUI package を追加
  - `tui/app.go`: Bubble Tea program 起動
  - `tui/state.go`: `~/.clawlet/tui/projects.json` 読み書き
  - `tui/client.go`: Unix domain socket HTTP client
  - `tui/project.go`: project discovery / health model
  - `tui/model.go`: Project List / Chat 画面の Bubble Tea model
- Charmbracelet 依存を追加
  - `github.com/charmbracelet/bubbletea v1.3.10`
  - `github.com/charmbracelet/bubbles v1.0.0`
  - `github.com/charmbracelet/lipgloss v1.1.0`
- TUI state を実装
  - 保存先: `~/.clawlet/tui/projects.json`
  - `path` を絶対パスに正規化
  - project ごとに `session_key` と `last_opened_at` を保持
  - `session_key` 未設定時は `default`
- Project discovery を実装
  - state に保存済みの project
  - current working directory
  - `~/.clawlet/workspace`
  - ユーザー入力で追加した path
- Gateway health check を実装
  - `{workspace}/.clawlet/gateway.sock` を検出
  - `GET /api/health` を呼び出し
  - `online`, `offline`, `stale socket`, `timeout`, `error` を区別
  - stale socket は削除せず状態表示のみ
- Chat API client を実装
  - `POST /api/chat`
  - request で `session_key` を明示送信
  - response の `content` / `tools_used` を表示
  - chat request 中は loading 表示
- Project List 画面を実装
  - project path / Gateway status / pid / workspace / active session key を表示
  - `Enter`: online project に接続
  - `r`: 再スキャン
  - `a`: project path を入力して追加
  - `d`: project list から削除
  - `q`: 終了
  - offline project 選択時は `clawlet gateway --dir <project>` を表示
- Chat 画面を実装
  - project path / session key / Gateway status / pid を表示
  - conversation log を表示
  - message input を表示
  - latest `tools_used` を表示
  - `Enter`: message 送信
  - `Esc`: Project List に戻る
  - `Ctrl+S`: session key 変更
  - `Ctrl+R`: health 再取得
  - `Ctrl+L`: 画面クリア
  - `Ctrl+C`: 終了

確認済み：

```bash
go test ./...
go vet ./...
go install ./cmd/clawlet
```

手動確認：

```bash
clawlet gateway --dir /Users/vps/dev/my-hobby/clawlet
clawlet tui
```

- Project List で `/Users/vps/dev/my-hobby/clawlet` が online として表示されることを確認
- Project を選択して Chat 画面に遷移できることを確認
- message を送信して assistant response / `tools_used` が表示されることを確認
- project session が `.clawlet/sessions/default.jsonl` に保存されることを確認

#### 未実装 / MVP 後に残す項目

- session 一覧 UI / session 作成・削除 UI
- session 履歴の読み込み表示
- 複数 Gateway への同時接続
- Gateway 自動起動
- tool execution / assistant delta の streaming 表示
- cron / heartbeat 管理 UI
- config.json 編集 UI
