---
name: test-first
description: Test-first workflow and conventions for writing tests: test behavior, not implementation; write acceptance scenarios before code; see red before green. Use when the user asks to write or add tests, or starts a new feature.
---

# test-first

## Workflow

1. Write the acceptance scenario first: title it "subject + behavior + condition" and structure it as Given / When / Then.
2. Run the test and make sure it **fails first** (red), proving it really checks something.
3. Write the least code that makes it pass (green), then refactor, keeping the tests green throughout.

## Principles

- Test behavior, not implementation: refactoring shouldn't break tests.
- Run the test level that matches the change instead of the full suite every time; see `references/test-levels.md`.
- See [BDD scenarios](references/bdd-scenarios.md) for writing acceptance scenarios and [lint rules](references/lint-rules.md) for lint rules.

```ts
it('sends a signed-out user from the orders page to sign-in', async () => {
  // Given signed out
  await logout()
  // When opening the orders page
  await page.goto('/orders')
  // Then lands on sign-in
  expect(page.url()).toContain('/login')
})
```
