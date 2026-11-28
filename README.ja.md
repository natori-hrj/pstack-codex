# nstack

[English](README.md)

**Go deep. Build less. Verify more.**

nstack は、Lauren Tan (Poteto) の pstack を土台にした Go ベースのエンジニアリングツールキットです。共通のワークフロースキル14件、原則12件、プレイブック7件を収録しています。観測可能な受け入れ条件を定め、必要な変更を絞り、候補そのものを検証します。

v0.4.1 から、ビルド・テスト・CI・インストール・自動実行は Go 実装と共通資産に統一され、Python と PyYAML は不要です。旧実装は公開済みタグに残ります。

| ハーネス | Workflow Support | Native Automation |
|---|---|---|
| [Codex](docs/codex.ja.md) | スキル、ロール、手動ワークフロー | Codex Adapter を使う Go 版 `nstack review`・`check-role` |
| [OpenCode v2](docs/opencode.ja.md) | 管理されたスキル導入と操作者主導のワークフロー | 未対応（[#12](https://github.com/natori-hrj/nstack/issues/12)） |
| Claude Code | 未対応 | [将来対応 (#13)](https://github.com/natori-hrj/nstack/issues/13) |
| Grok Build | 未対応 | [将来対応 (#14)](https://github.com/natori-hrj/nstack/issues/14) |

**Workflow Support** は共通スキルの導入とホストでの実行手順です。**Native Automation** はアダプターがレビュー・納品の品質契約を強制する経路です。OpenCode の独立レビューは操作者が別の読み取り専用セッションに引き継ぎます。nstack はその出力スキーマ、並列書き込み隔離、自動納品を保証しません。自動実行に必要な能力が不足する場合は停止します。

## 単一バイナリで導入

[リリース配布物](https://github.com/natori-hrj/nstack/releases)の対象バイナリを SHA-256 で検証し、`nstack` として使います。Linux amd64 は実行検証済み、Linux arm64 はクロスビルドのみです。他の OS は非対応です。`nstack --version` はバージョンと同梱資産のチェックサムを表示します。

Codex への導入:

ロール例3件も導入する場合は、実行前にシェルで `NSTACK_INSTALL_EXAMPLE_ROLES=1` を環境変数として設定します。コピーは有効化の成功後にだけ行い、既存のロールファイルとシンボリックリンクは上書きしません。

```sh
(
  set -e
  config_dir="${CODEX_HOME:-$HOME/.codex}"
  snapshot="$(nstack --version)"
  snapshot="${snapshot##*sha256:}"
  nstack install --home "$config_dir" stage "$snapshot"
  nstack install --home "$config_dir" activate "$snapshot"
  if [ "${NSTACK_INSTALL_EXAMPLE_ROLES:-0}" = 1 ]; then
    mkdir -p "$config_dir/agents"
    for agent in "$config_dir/nstack/snapshots/$snapshot"/examples/agents/*.toml; do
      dest="$config_dir/agents/${agent##*/}"
      if [ ! -e "$dest" ] && [ ! -L "$dest" ]; then
        (set -C; cat "$agent" > "$dest")
      fi
    done
  fi
)
```

新しい Codex セッションで `$setup-nstack` を実行し、受け入れ条件を添えて `$natori-mode` を起動します。OpenCode の導入・ロール設定・`@natori-mode` は [OpenCode ガイド](docs/opencode.ja.md)を参照してください。モデルと推論強度はユーザーが選びます。インストーラーはロールや認証設定を書き換えません。

更新は新しい ID をステージして有効化し、切り戻しは以前の完全な ID を有効化します。Codex と OpenCode は別々の設定先で共存できます。従来の直接配置・プラグイン・Python 版からは[移行ガイド](docs/migration.ja.md)を参照し、有効化前に重複を解消して、ホストが解決したパスを確認してください。

バイナリの実行に Python・PyYAML・Go は不要です。Workflow Support には対象ハーネス、自動レビューには Git、許可済み OAuth で使う Codex CLI と品質ゲートのツール、PR 納品には `gh` が必要です。インストーラーには Linux の procfs、flock、原子的 rename、ユーザー拡張属性と、実行ユーザー所有で他者が書き込めない管理ディレクトリが必要です。中断時に探索対象外へ作業ファイルが残る場合があります。原子的な切り替えは電断時の耐久性を保証しません。

## 開発と検証

Linux 上で Git と Go 1.25 以降（CI は 1.27.4）を使います。Go モジュールは同梱済みです。race test には C コンパイラが必要です。

```sh
go vet -mod=vendor ./...
go test -mod=vendor ./...
CGO_ENABLED=1 go test -mod=vendor -race ./...
```

CI は PR の head または `main` に push された SHA を検証します。`common` は Go テスト、race test、再現ビルドを、`validate` は追加で[実 Codex のオフライン検証](docs/codex.ja.md#検証と測定)を実行します。テストには一時 Git リポジトリとプロトコル fixture を使います。認証済みモデルの検証は別です。

[v0.4.1 リリースノート・再現ビルド](docs/releases/v0.4.1.ja.md) · [アダプター契約](docs/adapters.ja.md) · [スキル一覧](docs/skills.ja.md) · [上流と出典](docs/upstream.ja.md) · [MIT](LICENSE) · [上流 MIT](LICENSE-upstream) · [v0.3.0 参照資料](docs/v0.3.0.md)
