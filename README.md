# aicommit

`aicommit` is a small command line tool for generating commit messages. There
are many of these already out there, some even with the same name. But none
(to my knowledge) use the existing style of the repository. Following
repository conventions is essential for such a tool to be broadly useful.

## Install

```
go install github.com/coder/aicommit/cmd/aicommit@main
```

## Usage

You can run `aicommit` with no arguments to generate a commit message for the
staged changes.

```
aicommit
```

You can "retry" a commit message by using the `-a`/`--amend` flag.

```
aicommit -a
```

You can dry-run with `-d`/`--dry` to see the ideal message without committing.

```
aicommit -d
```

Or, you can point to a specific ref:

```
aicommit HEAD~3
```

in which case it will only generate a message for the old commit and not modify
the repository to prevent havoc and chaos in your git history.