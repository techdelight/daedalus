# Code Review

Review recent changes and provide constructive feedback.

## Instructions

1. Run `git diff HEAD~1` (or the appropriate range) to see the changes.
2. For each changed file, evaluate:
   - **Correctness**: Does the code do what it claims?
   - **Clarity**: Are names intention-revealing? Is the logic easy to follow?
   - **Tests**: Are changes covered by tests?
   - **Security**: Any potential vulnerabilities (injection, XSS, etc.)?
   - **Style**: Does it follow the project's conventions?
3. Summarize findings as a list of observations, ordered by importance.
4. Suggest specific improvements where applicable.
