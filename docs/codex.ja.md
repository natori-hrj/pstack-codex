# Codex アダプター

[English](codex.md)

スキルは [Go バイナリ](../README.ja.md)で導入します。カスタムロールが必要なら、ステージ済みスナップショットの `examples/agents/` から必要な TOML を `${CODEX_HOME:-$HOME/.codex}/agents/` にコピーします。既存ファイルやリンクは上書きせず、モデルと推論強度は自分で選択してください。

| 上流の手段 | Codex での扱い |
|---|---|
| スキル呼び出し | `$名前`。別の手順を参照する場合は対象の SKILL.md またはプレイブックを読む |
| Task / model rules | 利用可能なネイティブサブエージェントを名前で選ぶ。ロールのモデルと推論強度は TOML |
| 並列書き込み | 担当ごとに固有のブランチと Git worktree を割り当て、`codex -C <worktree>` で別セッションを開く |
| 読み取り専用レビュー | 読み取り専用の親セッションから呼ぶ `nstack_review`、または `codex exec -C <worktree> --sandbox read-only -m <model>` |
| スキーマ付き出力 | 必要なときに `codex exec --output-schema <file>` |
| セッション再開 | `codex resume <session-id>`。非対話セッションは `codex exec resume <session-id>` |
| Cursor Cloud、Bugbot、Automations、トランスクリプト連携 | 対象外。利用できる CI・レビュー証跡を読み取り、状態を報告する |

CLI 0.172.0 のヘルプには管理 worktree 用の `--worktree` もあります。手動 Git worktree は同じ起点と担当ブランチを明示する必要のあるタスクに使います。Git worktree 自体は権限境界ではありません。各セッションの実効サンドボックスを確認し、兄弟ディレクトリを `--add-dir` で書き込み対象に加えないでください。同じファイルを変更するタスクは直列化します。権限やモデルがホスト設定で固定されている場合はその制約を記録します。

納品前に最終候補の revision を固定し、必須品質ゲートを一度通します。候補が変わった場合は影響する証跡を更新します。コミット、担当ブランチへの push、PR 作成はリポジトリの権限に従います。リリース公開、マージ、破壊的な後片付けは明示的な許可を得ます。持ち込みの設定、フック、依存コマンド、エージェント出力は入力として扱い、実行前に確認します。パッケージにフック、MCP、API キー、外部サービス連携は含まれません。

## 検証と測定

`go test -mod=vendor ./...` はスキル一覧、マニフェスト、TOML、インストール、Git と配信、Codex プロトコルの fixture を確認します。Git fixture ではチェックアウトの動作を検証します。実 Codex CLI 0.172.0 で探索・設定・サンドボックス・キャンセルを認証なしで確認するには `NSTACK_CODEX_NATIVE_TEST=offline go test -mod=vendor -v ./internal/codex -run '^TestNativeOffline'` を使います。別の `NSTACK_CODEX_NATIVE_TEST=oauth go test -mod=vendor -v ./internal/codex -run '^TestNativeOAuth$'` は既存 OAuth アカウントの利用許可を得てから実行し、使い捨てデータで Sol と Astra の応答を確認します。

ライブでは CLI の明示的な `-m` で3モデルそれぞれが応答を完了しました。これは要求したモデル名で CLI リクエストが成功した証拠です。バックエンドの実体を独立に識別する証拠はありません。リリース前の追加検証では、ユーザー用 agents ディレクトリからネイティブのロール名を指定し、Sol と Astra の応答とセッション記録のモデル名を確認しました。Daybreak Blue は、一時環境でプログラムを指定しなかったときに `access_programs.cyber=daybreak_blue` を要求されました。認証済みの Codex の `model/list` で対象モデルの `availableAccessPrograms.cyber` に `daybreakBlue` があることを確認し、CLI 0.172.0 の `--cyber-access-program daybreak_blue` を指定した読み取り専用セッションで、ネイティブの `pstack_security` がモデル上書きなしで応答することを確認しました。アクセスプログラムの選択は利用権限を付与しません。権限が確認できない場合はそのロールの作業を止めます。

比較実験では各実行の開始・終了時刻を記録し、`codex exec --json` の `turn.completed.usage` で入力、キャッシュ済み入力、出力を集計します。非キャッシュ入力は入力からキャッシュ済み入力を引きます。担当の起動回数、レビュー回数、人間の介入、受け入れ検証まで完了したタスク数を同じ基準で手集計します。セッション ID やログはリポジトリの外に置きます。同条件の比較結果が揃うまで効率改善は判断しません。

[公式 Skills](https://learn.chatgpt.com/docs/build-skills) · [公式 Subagents](https://learn.chatgpt.com/docs/agent-configuration/subagents) · [公式 Plugins](https://learn.chatgpt.com/docs/plugins) · [公式設定リファレンス](https://learn.chatgpt.com/docs/config-file/config-reference)

| ライブ実行 | 入力 | キャッシュ済み | 非キャッシュ | 出力 |
|---|---:|---:|---:|---:|
| Sol 応答 smoke | 19,972 | 7,296 | 12,676 | 5 |
| Astra 応答 smoke | 19,921 | 7,296 | 12,625 | 5 |
| Daybreak Blue 応答 smoke | 15,517 | 11,392 | 4,125 | 5 |
| Astra の独立レビュー | 61,342 | 47,872 | 13,470 | 204 |
| 合計 | 116,752 | 73,856 | 42,896 | 219 |

独立レビューは `$interrogate` を読み、`total(2, 3)` の実測値 −1 と期待値 5 の差を報告して1回で終了しました。ライブ実行は4回、レビューは1回、fixture の検証済み並行タスクは2件。人間の介入回数と各ライブ実行の正確な所要時間は記録していません。

[公式 Daybreak CLI ガイド](https://help.openai.com/en/articles/20001607-using-daybreak-in-codex-cli)

## 起動前のロール確認

導入した TOML と、対象スコープでの検出を確認します。同じアカウントとクライアントの app-server で `includeHidden: true` を付けて `model/list` を取得し、全ページを `{"data": [...]}` にまとめます。`nstack check-role <導入済みロール.toml> < model-list.json` を実行します。モデルが継承設定なら実セッションで解決し、`--inherited-model` と、該当する場合は `--inherited-effort` に渡します。モデル一覧は公示されたアクセスの証拠です。ライブゲートが必要な場合は、応答完了を別途記録します。

`adapters/codex/optional/nstack_security_daybreak.toml` は利用する場合に agents ディレクトリへ配置します。許可された防御作業に明示的に選択し、事前確認に `--daybreak-selected` を付けます。`availableAccessPrograms.cyber` に `daybreakBlue` があることを確認します。クライアントが要求する場合は、そのセッションに `--cyber-access-program daybreak_blue` を指定します。グローバル既定は無効のままにします。ロール、モデル、アクセスが不足している場合は起動を停止し、理由を報告します。モデルの代替には明示的な選択が必要です。

## 独立レビューの自動実行

ネイティブサブエージェントは、設定済みモデルの選択と親への結果返却に対応します。実効サンドボックスと承認設定は親から継承します。親が読み取り専用ならこの方式を利用できます。書き込み可能な実装用の親では、レビューだけの読み取り専用を強制できないため、ホスト用コマンドはライターと各レビューを別のネイティブ app-server プロセスと一時スレッドで起動します。

Git、Codex CLI 0.172.0、既存の ChatGPT OAuth ログインがある許可されたホストで `nstack review` を実行します。専用のクリーンなフィーチャーチェックアウトと、観測できる受け入れ条件を含む依頼ファイルを使います。ホストは Codex の実行時状態を書き込め、通常のサンドボックスを起動できる必要があります。通常の Codex ホームと認証ストアを使います。チェックアウトごとに実効 MCP 設定を読み、Codex の `-c` で子プロセスにだけ上書きを適用し、起動前にすべての MCP サーバーが無効だと確認します。ユーザーやプロジェクトの設定ファイルは保持されます。子プロセスではアプリ、プラグイン、フック、メモリ、Web 検索、ネイティブ委任を無効にします。サンドボックスは書き込みとネットワークを制限します。ファイルの読み取りまで分離する必要がある場合は、制限された専用ホストアカウントを使ってください。

ネイティブレビューには Linux の pidfd（Linux 5.4 以降の `waitid(P_PIDFD)` を含む）、subreaper、および現在の PID 名前空間と各スレッドの `children` ファイルが見える procfs が必要です。`CODEX_EXEC_SERVER_URL` は設定せず、Codex の実行をローカルの管理対象プロセスツリー内に置きます。監視プロセスは子孫の停止・回収を確認してから終了を通知します。必要な機能や終了確認が得られない場合、ワークフローは失敗します。エラーは `status.json`、監視プロセスが出力した診断は該当段階の `.stderr` を確認してください。root 権限や cgroup の委任は不要です。

```sh
nstack review --authorize-host \
  --work-ref refs/heads/nstack-work-example \
  --repo /path/to/feature-checkout --base <基点SHA> \
  --brief /path/to/acceptance.md --evidence /path/to/new-private-directory \
  --implement-model gpt-6.2-sol --implement-effort medium \
  --review-model gpt-6.2-astra --review-effort medium \
  -- /bin/sh -c 'GOCACHE="$TMPDIR/go-cache" GOTOOLCHAIN=local go test -mod=vendor ./...'
```

既存コミットだけを検証する場合は `--review-only` を使います。書き込みを許可する場合は、まだ存在しないローカル ref を `--work-ref refs/heads/<work>` で指定します。必要になった時点で作業ブランチを作成し、元の ref は保持します。納品を許可する場合は `--push <remote> --delivery-ref refs/heads/<feature>` を指定します。作業 ref と配信 ref は別にします。既存 PR のみを更新するには `--github-repo <owner/repo> --pr-base <branch> --pr-number <number>` を指定します。PR の確認・作成を許可する場合は番号の代わりに `--pr-title <title>` を使います。

事前確認では、実効設定、認証、モデルと推論強度の提示、各スレッドの設定、レビュー時の実際の書き込み拒否、ゲート用の専用一時領域への書き込みを検証します。プロンプト送信前に実効スレッド設定を比較し、モデルが切り替わった場合は停止します。各候補のゲートは専用の一時領域を持つクリーンなチェックアウトで実行します。レビューは別のチェックアウトとコンテキストで開始し、判定に完全な候補 SHA を要求します。実証された指摘の修正は最大2巡です。ゲート失敗、証跡不足、レビュー保留、指摘の再発、認証・権限エラーでは納品を止めます。候補別のゲート出力、モデルとサンドボックス設定、スレッド・ターン ID、構造化判定、取得できた使用量、納品状態を証跡ディレクトリに記録します。リポジトリの内容を含む場合があるため Git の管理外に置きます。

納品時は Git の実効 push 先を1つに確定し、remote HEAD を検証できない場合は停止します。PR の差分は、レビュー対象の基点と候補 SHA に一致する必要があります。基点のブランチがローカルにない場合は、実行前に取得してください。レビュー専用モードで修正を求められた場合は非ゼロで終了し、`status.json` に `changes_requested` を記録します。ゲートコマンドの直前には `--` を置き、ゲートが使うツールを用意します。既存の Go バイナリ名 `nstack-codex` でも引数は同じです。

TOML パーサーを同梱しています。[MIT ライセンス通知](../vendor/github.com/BurntSushi/toml/COPYING)も参照してください。

## 既存候補を編集前にレビューする

クリーンなチェックアウトで `--review-first` を指定すると、既存の候補を検証します。初回合格なら作業ブランチを作りません。指摘がある場合は明示された作業 ref と、配信指定があれば実効 remote の既定ブランチ、配信 ref、開始 SHA、PR を照合してから修正します。再レビューは過去の指摘を ID ごとに `resolved`、`recurring`、`uncertain` と評価し、新規指摘も別途探索します。再発、判定不能、照合の不足があれば次の修正を止めます。指摘の意味の照合には誤りがあり得るため、修正上限は2巡です。生の応答と候補・基点 SHA は各レビューの証跡に残ります。

認証済みホストで、使い捨てリポジトリに対する実装先行フローを Sol と Astra の推論強度 `medium` で確認しました。`0069da1` の品質ゲートは成功し、Astra は修正を要求しました。Sol の修正後、`47d2db8` の品質ゲートが成功し、別の読み取り専用 Astra セッションがその SHA に合格を出しました。両レビューで書き込み拒否を確認しました。所要時間は54.1秒で、4ターンの入力は153,421トークン（うちキャッシュ済み90,624）、出力は1,335トークンでした。この証跡は nstack `3cf2feb` による実験のものです。
