# CLI Input Debug Plan

## 背景

`cmd/clawlet/input.go` で `golang.org/x/term` の raw モードによるマルチライン入力 (`Ctrl+J` 改行 / `Enter` 送信) を実装したが、以下の問題が確認された。

## 確認された問題

### 1. Enter していないのに自動送信される

**症状**: tmux 越しにテキストをペーストすると、`Enter` を押す前に送信される。

**推定原因**:
- SSH + tmux 環境では paste 時に `\r\n` (CR+LF) が入力される
- raw モードでは `0x0D` (CR = Enter) を受信すると即座に送信扱いになる
- ペーストデータ中の CR が「Enter 押下」と誤認される

**確認すべきこと**:
- tmux の `bracketed-paste` モードが有効か
- bracketed paste (`\x1b[200~` ... `\x1b[201~`) のエスケープシーケンスを受信できているか
- 端末が bracketed paste を送出しているか (`printf '\x1b[?2004h'` で有効化)

### 2. 前回の入力が次のプロンプトに残留

**症状**: 1回目の送信後、次の `--- user ---` の入力欄に前回のテキストが表示されたままになる。

**推定原因**:
- `render()` 内の `\x1b[0J` (cursor から画面末尾まで消去) が tmux 越しで期待通り動作していない
- `lastCurLine` の計算がずれ、カーソル移動量が誤っている
- あるいは `readMultiline` が返った後に端末状態が中途半端になっている（raw モード解除のタイミングでゴミが残る）

**確認すべきこと**:
- `readMultiline` 終了時（Enter 押下時）の端末状態: `\r\n` を出力して raw モードを抜けているが、残留文字がないか
- `defer term.Restore` の前に stdout に溜まったバッファがフラッシュされているか

## Debug 実装方針

### A. 入力バイト列のトレースログ

`input.go` に debug モードを追加し、受信した全バイトをファイルに記録する。

```go
// input.go に追加する debug 用の仕組み

var debugInputFile *os.File

func initDebugInput() {
    if os.Getenv("CLAWLET_DEBUG_INPUT") == "" {
        return
    }
    f, err := os.Create(os.Getenv("CLAWLET_DEBUG_INPUT"))
    if err == nil {
        debugInputFile = f
    }
}

func traceByte(b byte, label string) {
    if debugInputFile == nil {
        return
    }
    fmt.Fprintf(debugInputFile, "[%s] 0x%02X (%c)\n", label, b, safeChar(b))
}
```

### B. 状態遷移のトレース

各キー処理後に buffer / cursor の状態を記録:

```go
func traceState(phase string, buffer []rune, cursor int, lastCurLine int) {
    if debugInputFile == nil {
        return
    }
    fmt.Fprintf(debugInputFile, "[state:%s] buf=%q cursor=%d lastCurLine=%d\n",
        phase, string(buffer), cursor, lastCurLine)
}
```

### C. bracketed paste 対応

tmux の paste を正しく扱うには、bracketed paste シーケンスをパースする必要がある:

```
\x1b[200~  ...paste content...  \x1b[201~
```

paste 中の `\r` は改行として扱い（送信しない）、`\r\n` は `\n` 1つに正規化する。

ただし bracketed paste は端末側が対応している必要があり、有効化には `\x1b[?2004h` の送出が必要。

### D. レンダリングの堅牢化

- `\x1b[0J` の代わりに、明示的に1行ずつ `\x1b[2K` (行全体消去) + `\r\n` で消去する方式も検討
- 入力エリアの行数を常に過大評価してクリアする（多めに消す分には問題ない）
- `readMultiline` の呼び出し元で、入力前に `\x1b[0J` を一度打っておく

## 優先度の高い TODO

1. **`CLAWLET_DEBUG_INPUT` 環境変数でのバイトトレース実装**
   - 最も情報価値が高い。実際に何が送られてきているか記録できる。
   - tmux paste 時のバイト列がわかれば、bracketed paste 対応の要不要が判断できる。

2. **bracketed paste 対応（`\x1b[200~` / `\x1b[201~`）**
   - tmux が paste 時にこれを送出していれば、それを使って paste モードに入り、内部の CR を改行に変換する。
   - 送出していなければ `\x1b[?2004h` を有効化する。

3. **render 前後のクリーンアップ強化**
   - `runInteractive` 側で毎回 `\x1b[0J` を打ってから `readMultiline` に入る。
   - `readMultiline` 内の `render()` で、常に想定より1行多く上書きする安全マージンを入れる。

4. **単体テスト可能な入力パーサへの分離**
   - バイト列 → バッファ操作 のロジックを pure な関数に切り出し、raw モードや端末 I/O に依存しないテストを書く。
   - `input.go` の `inputState` のメソッド群は既に pure に近いので、テスト追加は容易。

## 現状のコード参照

| ファイル | 役割 |
|----------|------|
| `cmd/clawlet/input.go` | raw モードのマルチライン入力ハンドラ |
| `cmd/clawlet/cmd_agent.go` | `runInteractive` から `readMultiline()` を呼び出す |

## テスト方針

```go
// input_test.go (新規)

func TestInputState_InsertRune(t *testing.T) { ... }
func TestInputState_Backspace(t *testing.T) { ... }
func TestInputState_CtrlK(t *testing.T) { ... }
func TestInputState_CtrlU(t *testing.T) { ... }
func TestInputState_CtrlW(t *testing.T) { ... }
func TestInputState_MoveUpDown(t *testing.T) { ... }
func TestInputState_CursorPos(t *testing.T) { ... }
func TestInputState_LineStartEnd(t *testing.T) { ... }

// バイト列 → 操作 の変換をテスト
func TestHandleEscSeq(t *testing.T) { ... }
func TestDispatchByte(t *testing.T) { ... }
```

## 想定タイムライン

1. **即時**: `CLAWLET_DEBUG_INPUT` トレース実装 → 実環境でのバイト列取得
2. **バイト列確認後**: bracketed paste 対応 or paste CR→\n 変換
3. **並行**: render クリーンアップ改善
4. **安定後**: 単体テスト追加、TUI 移行の布石に
