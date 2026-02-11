# Contributing

Thanks for your interest in contributing! This guide will help you get started.

## Getting Started

All contributions go through the **fork and pull request** workflow.

### 1. Fork the Repository

Click the **Fork** button at the top right of the repository page to create your own copy.

### 2. Clone Your Fork

```bash
git clone https://github.com/mbd888/alancoin.git
cd alancoin
```

### 3. Add the Upstream Remote

This lets you keep your fork in sync with the original repo.

```bash
git remote add upstream https://github.com/mbd888/alancoin.git
```

### 4. Create a Branch

Always work on a feature branch, never directly on `main`.

```bash
git checkout -b your-branch-name
```

Use a descriptive branch name like `fix/login-bug` or `feature/dark-mode`.

## Making Changes

1. Keep your changes focused. One pull request per feature or fix.
2. Follow the existing code style and conventions in the project.
3. Write clear, concise commit messages.
4. Add or update tests if applicable.
5. Make sure existing tests pass before submitting.

## Staying Up to Date

Before pushing your changes, sync your fork with upstream to avoid merge conflicts:

```bash
git fetch upstream
git rebase upstream/main
```

## Submitting a Pull Request

1. Push your branch to your fork: `git push origin your-branch-name`
2. Go to the original repository and click **New Pull Request**.
3. Select your fork and branch as the source.
4. Fill out the PR template with a clear description of what you changed and why.
5. Link any related issues (e.g., "Closes #42").

## What to Expect

After submitting, your PR will be reviewed. You might be asked to make changes, but that's normal and part of the process. Please be patient, as reviews may take some time.

## Reporting Issues

If you find a bug or have a feature request, please open an issue first. Include as much detail as possible: steps to reproduce, expected behavior, screenshots, environment info, etc.
