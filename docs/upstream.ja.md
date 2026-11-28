# 上流・ライセンス・同期

[English](upstream.md)

基盤は [cursor/plugins の pstack 0.15.48](https://github.com/cursor/plugins/tree/b7b93c36be9bcc36a74db438e0fd385d660bc559/pstack)（`b7b93c36be9bcc36a74db438e0fd385d660bc559`）。`skills/principle-*/SKILL.md` の12件は上流の同名ファイルと同一です。その他のスキルとプレイブックは上流のワークフローに基づく Codex 向けの短縮実装です。上流の Claude Code アダプターと同様に、ホスト用語と機能を対応づけます。上流の生成スクリプト、フック、Bun ツール、PR watcher は同梱していません。

## 比較

固定 revision の公開アーカイブで README、マニフェスト、モデル・実行・検証の該当ファイルを比較しました。ポートのプログラムは実行していません。

| ポート | 構成とトレードオフ |
|---|---|
| [nahuelb](https://github.com/nahuelb/pstack-codex/tree/121c6fb0c47135a02ef927f76da76a3dbf176ff1) | 上流ロックと互換性レポート、CLI マーケットプレイス、役割レジストリからカスタムエージェント設定を管理。ネイティブ並行実行、worktree 監査、head 単位の検証、インストール smoke がある。フックと Benny 自動化まで含む構成。 |
| [atlys-hein](https://github.com/atlys-hein/pstack-codex/tree/b7bf49e08f756db153be29c6611bbb8d9d1136b5) | 上流 0.15.36 を基にしたインポート用パッケージ。役割・並行性はスキルの実行指示に記載し、Codex ホストが権限を制御。リソースマップ、ローカルチェックポイント、実行契約でファイル所有と親の検証を扱う。 |
| [theomilll](https://github.com/theomilll/pstack-codex/tree/270c71fa27e0f5128a073424e3e6fe3042606555) | CLI マーケットプレイスと上流追従チェック。ネイティブサブエージェント、worktree 監査、独立検証を採用。モデルと推論強度は親の設定を継承。フックとヘルパーも含む。 |
| [painhardcore](https://github.com/painhardcore/pstack/tree/af212355c0a8f21d2545f20042628f3911048a4f) | Codex、Claude Code、OpenCode を対象にしたパッチ同期方式。CLI マーケットプレイスと汎用 skills CLI。能力に応じた委任と直列フォールバック、プロジェクト検証スキル、配布ゲートを提供。モデルは原則継承。 |
| [bemoat](https://github.com/bemoat/pstack/tree/dc8e617f179cf0bdc68b61e672f33e9eb64c20cf) | 複数ホスト向けの生成器、モデル設定と worktree・再開用ヘルパー、生成・同期・モデルのテストがある。Codex の導入はマーケットプレイス経由。対象ホストが増える分、生成・テスト・実行依存の保守範囲が広い。 |

5件とも pstack 部分のルートライセンスは MIT。bemoat は Lauren Tan に加えて Michael Denyer の著作権表示があり、別の Cursor Team Kit 用ライセンスも持ちます。この配布のコード採用元は上流 pstack です。上流の MIT 表示は [LICENSE-upstream](../LICENSE-upstream) に保持します。直接上流を起点にしたことで、複数の同期層やフックを保守せず、当配布の役割設定と公開権限を明示できます。

## 同期

1. 上流の新しい SHA を確認し、現在の SHA と pstack、生成スクリプト、マニフェスト、ライセンスの差分を読む。
2. 採用済み原則の更新を確認し、同じ相対パスから必要な原文を取り込む。手順の変更は該当 Codex スキルに限定して反映する。
3. 追加の実行手段や外部接続が含まれる場合はコマンド、権限、対象、ライセンスを確認する。
4. この文書の SHA、バージョン、変更した出典と必要な著作権表示を更新し、`go test -mod=vendor ./...` と関係する短い smoke を実行する。
