# cost-aware-summarizer

技術記事の RSS を収集し、LLM で要約して Discord に通知する CLI。1 回実行して終了するジョブとして動作し、GitHub Actions の cron で定期実行する想定。

## 目的

LLM の呼び出しコストを抑えつつ、Gemini / GitHub Actions / ghcr.io の無料枠の中で運用する。既読フィルタ、1 実行あたりの処理上限、安いモデルで足切りしてから高性能モデルで要約する 2 段カスケードによって、高コストな要約処理に到達する記事数を減らす。

## 機能

- 複数の RSS/Atom フィードから記事を取得する
- 処理済みの記事を SQLite で管理し、既読はスキップする（LLM を呼ばない）
- 1 実行あたりの新着処理件数に上限を設ける
- Gemini Flash で「要約する価値があるか」を判定し（1 段目）、価値のある記事だけ Gemini Pro で詳細要約する（2 段目）
- 判定・要約・タグを SQLite に保存する
- 要約結果を Discord の Incoming Webhook に通知する

## 外部エンドポイント

- Gemini API: `POST https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent`
- Discord: Incoming Webhook（`DISCORD_WEBHOOK_URL` へ POST）
- RSS フィード: `FEED_URLS` で指定（既定は Zenn とはてなブックマーク IT）

## 実行

ローカル:

```bash
export GEMINI_API_KEY="..."
export DISCORD_WEBHOOK_URL="..."   # DRY_RUN=true なら不要
export FEED_URLS="https://zenn.dev/feed"
export MAX_ARTICLES_PER_RUN=3
go run ./cmd/summarizer
```

Docker:

```bash
docker build -t cost-aware-summarizer:dev .
docker run --rm \
  -e GEMINI_API_KEY="$GEMINI_API_KEY" \
  -e DISCORD_WEBHOOK_URL="$DISCORD_WEBHOOK_URL" \
  -e FEED_URLS="https://zenn.dev/feed" \
  -e MAX_ARTICLES_PER_RUN=3 \
  -v "$(pwd)/data:/data" \
  cost-aware-summarizer:dev
```

イメージは scratch ベースで約 12.4MB。`-v` で `data/` をマウントすると既読 DB がコンテナ外に残り、次回実行で既読判定が効く。

## 設定（環境変数）

| 変数 | 既定値 | 説明 |
| --- | --- | --- |
| `GEMINI_API_KEY` | （必須） | Gemini API キー |
| `DISCORD_WEBHOOK_URL` | （`DRY_RUN=false` のとき必須） | Discord Incoming Webhook |
| `FEED_URLS` | Zenn / はてな IT | カンマ区切りの RSS URL |
| `DB_PATH` | `./data/summarizer.db` | SQLite ファイルのパス |
| `GEMINI_TRIAGE_MODEL` | `gemini-2.0-flash` | カスケード 1 段目 |
| `GEMINI_SUMMARY_MODEL` | `gemini-2.5-pro` | カスケード 2 段目 |
| `MAX_ARTICLES_PER_RUN` | `10` | 1 実行あたりの新着処理上限 |
| `MAX_CONTENT_CHARS` | `4000` | LLM へ送る本文の最大文字数 |
| `HTTP_TIMEOUT` | `30s` | 外部 HTTP のタイムアウト |
| `RUN_TIMEOUT` | `5m` | 実行全体のタイムアウト |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `DRY_RUN` | `false` | 通知をスキップ（取得・要約・保存は実行） |

## テスト

```bash
go test ./...
```

LLM クライアントは interface 化してあり、テストではモックを使うため実 API を叩かない。`store`（既読判定・upsert）と `pipeline`（カスケード全体）をカバーする。

## 技術スタック

Go 1.25 / Gemini API（net/http で直接呼び出し）/ SQLite（`modernc.org/sqlite`、純 Go・CGo 不要）/ `mmcdole/gofeed` / `caarlos0/env` / `log/slog` / Docker（マルチステージ + scratch）/ GitHub Actions + ghcr.io。

## ライセンス

MIT
