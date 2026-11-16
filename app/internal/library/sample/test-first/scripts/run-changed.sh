#!/bin/sh
# 只运行改动文件对应的测试
git diff --name-only HEAD | grep -E '\.(ts|tsx)$' | xargs -r npx vitest related --run
