# ClaukaCode (cc-ukagaka)

Claude Code のフックイベントを SSTP `NOTIFY` に変換し、伺か（SSP）のゴーストに反応させる連携ツールです。

## 構成

- `main.go` — `cc-ukagaka`: Claude Code の hook から都度起動され、SSTP NOTIFY を SSP (`127.0.0.1:9801`) へ送る単一バイナリ（Go, 標準ライブラリのみ）。
- `examples/claude-settings.hooks.json` — `~/.claude/settings.json` に貼る hooks 設定（必須イベント）。
- `examples/claude-settings.hooks.pretooluse.json` — 任意の `PreToolUse` フック設定。
- `claude_mascot/` — 里々ゴースト用の受け口辞書等。`satori.dll` とシェル（立ち絵）は各自で配置してください。

## ビルド

```sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o cc-ukagaka.exe .
```

## セットアップ

1. `cc-ukagaka(.exe)` を PATH の通ったディレクトリに配置する。
2. `examples/claude-settings.hooks.json` の内容を `~/.claude/settings.json`（または `<project>/.claude/settings.json`）の `hooks` にマージする。既存の `hooks` があれば上書きせずマージすること。
3. SSP を起動し、`claude_mascot/` を `.nar` 化したゴースト（`satori.dll` と shell を追加した状態）を導入・起動する。

## 他のエージェント（Codex / Pi / OpenCode）

バイナリを置いた状態で、次を実行するとアダプタが配置されます（バイナリの絶対パスを埋め込むため PATH 設定は不要。Windows / macOS 共通）。

```sh
cc-ukagaka install codex     # $CODEX_HOME または ~/.codex/hooks.json にマージ（要 config.toml の [features] hooks = true）
cc-ukagaka install pi        # ~/.pi/agent/extensions/cc-ukagaka.ts
cc-ukagaka install opencode  # ~/.config/opencode/plugins/cc-ukagaka.js
```

対象エージェントを一度起動して設定ディレクトリが存在する状態で実行してください。再実行は上書き（codex は既存の cc-ukagaka エントリを置換、他のフックは保持）。アンインストールは該当ファイル／エントリを手動で削除してください。

## 環境変数

| 変数 | 既定 | 用途 |
|---|---|---|
| `CC_UKAGAKA_HOST` | `127.0.0.1` | 送信先ホスト（WSL2 越境時に Windows ホスト IP を指定） |
| `CC_UKAGAKA_PORT` | `9801` | SSTP ポート |
