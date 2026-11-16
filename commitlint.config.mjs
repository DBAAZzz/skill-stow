// 提交信息规范：Conventional Commits（https://www.conventionalcommits.org/zh-hans/）。
// 格式：<type>(<scope>): <subject>，例如 fix(usage): keep 批量删除 reachable
// type 取 feat / fix / docs / style / refactor / perf / test / build / ci / chore / revert。
export default {
  extends: ['@commitlint/config-conventional'],
}
