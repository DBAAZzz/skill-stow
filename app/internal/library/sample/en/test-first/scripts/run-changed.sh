#!/bin/sh
# Run only the tests related to changed files
git diff --name-only HEAD | grep -E '\.(ts|tsx)$' | xargs -r npx vitest related --run
