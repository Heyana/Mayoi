# Handoff: Mayoi Fork 状态与上游 PR 拆分计划

## 1. 当前仓库与安装状态

- **当前分支**：`feat/commit-stats`（已推送至 `origin/feat/commit-stats`，工作区干净）
- **最新提交**：`afa88f5`
- **上游远端**：`upstream` → `https://github.com/sorrycc/Mayoi.git`
- **本机安装**：已构建并安装最新 `Mayoi.app` 到 `/Applications/Mayoi.app`

### 本次会话完成的 3 个提交
| Commit | 内容 | 验证 |
|---|---|---|
| `2553ba0` | **一键初始化 & 发布仓库**：新增 `Git: Initialize Repository` / `Git: Publish Repository…`、`gh repo create` 私有/公开弹窗、无 remote 入口及三语 | 单测 + UI 测试 + `lint.sh` 通过 |
| `ecd21cc` | **轮询与功耗优化**：`railChanges` 接入 `readGit` 并发限流与激活项目复用；无 `hc` 停掉 Sessions 3s 空转；窗口失焦时暂停 Git/GitHub/PR/Sessions 后台周期唤醒 | `TestPolling*` / `TestRail*` + `lint.sh` 通过 |
| `afa88f5` | **Project Info 项目信息弹窗**：项目栏右键 / 视图菜单查看总代码行数、文件数、体积、语言占比条、24h/7d/30d 变动与 Git 仓库概览（豆包执行 + PM Review 修复 3 处细节） | `TestScanProjectInfo*` / `TestProjectInfo*` + `lint.sh` 通过 |

---

## 2. 上游 PR 拆分计划（`sorrycc/Mayoi`）

> [!IMPORTANT]
> 不要直接把 `feat/commit-stats` 整体提 PR；每个 PR 都应从 `upstream/main` 新拉干净分支，按主题拆分、独立验证并只保留该主题相关的 `CHANGELOG.md` 条目。

### 第一批（Wave 1 · 3 个 PR，优先提）

1. **PR 1 · 后台轮询静默 & 无 `hc` 停止 3 秒空转**
   - 建议分支：`pr/pause-background-polling`
   - 来源提交：`ecd21cc`（仅提取 `app.go`、`git.go`、`github.go`、`pullrequest.go`、`commitchecks.go`、`prchecks.go`、`scmworktrees.go`、`sessions.go`、`polling_test.go`；剥离 fork 专属的 `projects.go` rail 改动）
   - 卖点：窗口失焦后真正静默省电、修复未用 `herdr-core` 时 Sessions 面板每 3s 空转重绘。

2. **PR 2 · Diff 宽字符（CJK / Emoji / 组合字符）换行修复**
   - 建议分支：`pr/diff-wide-char-wrap`
   - 来源提交：`974a8f5` + `ed0f270` 中的 diff wrapping 修复部分
   - 卖点：窄 diff 面板下中文、Emoji 和组合字符不再右侧裁切或拆碎。

3. **PR 3 · 一键初始化 & 发布仓库到 GitHub**
   - 建议分支：`pr/init-and-publish-repo`
   - 来源提交：`2553ba0`（几乎可直接 cherry-pick 到 `upstream/main`）
   - 卖点：复用现有 `gh` CLI 基础设施，补齐 `Git: Initialize Repository` 和 `Git: Publish Repository…`（含私有/公开切换、无 commit 跳过 `--push`、三语和单测）。

### 第二批（Wave 2 · 3 个 PR，第一批有反馈后再提）

4. **PR 4 · Source Control 增删行数 & 多选提交区间 diff**
   - 建议分支：`pr/scm-line-stats-and-range-diff`
   - 来源提交：`7e9639e`、`8eb8c86`、`8011742`、`65a2ebb`、`50ab6ab`（附带 `4e59348` 中 commit stat cache 修复）

5. **PR 5 · Markdown 记忆模式 & 单文件 Diff Reading 切换**
   - 建议分支：`pr/markdown-mode-and-diff-reading`
   - 来源提交：`677df22`、`e167354`、`a4887fd`（附带 `ed0f270` 中 Markdown 状态修复）

6. **PR 6 · Project Info 项目信息与代码统计弹窗**
   - 建议分支：`pr/project-info-dialog`
   - 来源提交：`afa88f5`（基于 `upstream/main` 时需去掉 `projectInfoRecent` 对 `stats.go` 的依赖，保持完全自包含）

### 随时可插空单发的 2 个小 PR
- `223fc21`：删除最后一个 linked worktree 时刷新 worktree 计数徽章
- `fd925d9`（+ `ed0f270` 相关修复）：Solarized Light 主题

### 暂不直接提 PR（先开 Discussion / 留在 fork）
- **左侧项目栏大改版 + `mayoi projects` CLI**（`1169717` ~ `4b0d558` 等）：改动面大、涉及交互偏好，建议带截图先在上游开 Discussion 确认意向。
- **标题栏 Recent Commits（24h/7d/30d）统计面板**（`98bb1a4` ~ `7d00828`）：建议先问上游是否收录。
- **字号微调**（`1ab8a84`）：纯个人偏好，留在 fork。

---

## 3. 下次开工执行步骤

1. `git fetch upstream main`
2. 按 Wave 1 顺序，用独立 worktree 或分支从 `upstream/main` 拆出分支：
   ```bash
   git worktree add ../Mayoi-pr-1 -b pr/pause-background-polling upstream/main
   ```
3. 提取对应改动后执行验证：
   - 若改动 `locales/*.json`：`go run github.com/egoist/mygo/cmd/mygo generate`
   - 定向 `go test -v -run '<TestPattern>' ./cmd/mayoi ./internal/git`
   - `sh scripts/lint.sh`（确认 `lint ok`）
4. 推送分支到 `origin` 并用 `gh pr create --repo sorrycc/Mayoi` 发起 PR（附简短说明与截图）。
