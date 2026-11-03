---
name: dependency-updates
description: How to update Go module dependencies safely. Use when bumping versions.
---
# Dependency updates

## When this applies
Use this skill whenever the task involves dependency updates. If the task only touches dependency updates incidentally, read the checklist below and skip the rest.

## Before you start
1. Read the existing code and configuration that relate to dependency updates; follow the patterns already in use rather than introducing new ones.
2. Check the project's AGENTS.md for overrides of anything below.
3. Identify who reviews changes to dependency updates in this repository and what they expect to see in the change description.

## Procedure
1. Make the smallest change that accomplishes the goal; avoid unrelated cleanups in the same change.
2. Keep configuration in version control. Never commit secrets, tokens, or credentials; reference them through the existing secret store.
3. Write down any manual step you had to perform, so it can be automated later.
4. Verify the change locally with the narrowest command that exercises it, then with the full test suite.
5. Describe in the change summary what you changed, how you verified it, and what could go wrong after release.

## Checklist
- [ ] The change follows the existing conventions for dependency updates.
- [ ] There is a way to roll the change back, and the summary says what it is.
- [ ] Tests or checks cover the new behavior, including at least one failure case.
- [ ] Logs and error messages are specific enough to diagnose a failure without attaching a debugger.
- [ ] Documentation that mentions dependency updates is updated in the same change.

## Common mistakes
- Changing behavior and refactoring in one step, which makes the change hard to review and hard to revert.
- Verifying only the happy path.
- Leaving temporary debugging code or overly verbose logging behind.
- Assuming an environment variable or file exists in every environment because it exists locally.
