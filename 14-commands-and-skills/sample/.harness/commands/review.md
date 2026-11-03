---
description: Review uncommitted changes for bugs
argument-hint: [focus]
---
Review the uncommitted changes in this repository. Focus: ${1:-correctness and missing tests}.

Current changes:
!`git diff --stat`

Read the changed files, then list concrete problems with file:line references. Do not edit anything.
