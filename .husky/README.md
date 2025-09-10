# Git Hooks

This directory contains Git hooks managed by Husky.

## Pre-commit Hook

The `pre-commit` hook automatically runs before each commit to:

1. **Lint and fix** TypeScript/JavaScript files with ESLint
2. **Format** all supported files with Prettier
3. **Stage** the fixed/formatted files

### What gets processed:

- **`*.{js,jsx,ts,tsx}`**: ESLint fix + Prettier format
- **`*.{json,css,scss,md}`**: Prettier format only

### How it works:

1. You run `git commit`
2. Husky triggers the pre-commit hook
3. lint-staged processes only staged files
4. ESLint fixes linting issues
5. Prettier formats the code
6. Fixed files are automatically staged
7. Commit proceeds if no unfixable errors

### Bypassing hooks (not recommended):

```bash
git commit --no-verify -m "emergency commit"
```

## Setup

Hooks are automatically installed when you run:

```bash
npm install
```

The `prepare` script in package.json ensures Husky is set up after installation.
