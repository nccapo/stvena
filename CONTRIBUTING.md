# Contributing to Stvena

Thanks for helping improve Stvena. Small fixes, reproducible bug reports, and
feedback from real terminal workflows are useful contributions.

## Work locally

Use Go 1.24.2 or newer, Git, and macOS or Linux. Clone your fork, create a branch,
then build and run the checks:

```sh
go build -o bin/stvena ./cmd/stvena
go test ./...
go test -race ./...
go vet ./...
```

Run the built executable from a disposable Git repository for interactive
testing. An installed agent is needed to exercise the agent pane; standalone
`stvena review` can exercise the review interface. For UI changes, check narrow
and wide terminals, keyboard and mouse input, and return of focus to the agent.

Format changed Go files with `gofmt`. Add regression coverage when changing
behavior, and keep pull requests focused on one problem. Describe the trigger,
the resulting behavior, and what you verified. Discuss substantial features in
an issue before investing in a large implementation.

## Report a bug

Include your OS, terminal, Go version, Stvena commit, agent CLI and version,
reproduction steps, and expected versus actual behavior. Prefer a minimal
repository with synthetic data. Redact credentials, private source, personal
paths, prompts, and account information from logs or recordings.

## Before committing

Review `git diff --cached` and the list of staged paths. The repository ignores
common credential files, local agent state, build artifacts, and recordings,
but ignore rules do not protect files already tracked by Git.

With [Gitleaks](https://github.com/gitleaks/gitleaks) installed, scan locally:

```sh
gitleaks dir . --redact --no-banner
```

Never commit real credentials or sensitive test fixtures. Use ordinary branch
pushes: `git push --mirror` would also publish Stvena's local snapshot refs.

## Publish a release

Release builds are created for macOS and Linux on amd64 and arm64. After CI
passes on the release commit, push a semantic version tag:

```sh
git tag -a v0.1.0 -m "Stvena v0.1.0"
git push origin v0.1.0
```

The release workflow runs the tests, creates the GitHub release, and uploads the
archives and checksum file consumed by `install.sh`. Test the installer against
the new version before updating release announcements:

```sh
STVENA_VERSION=v0.1.0 STVENA_INSTALL_DIR="$(mktemp -d)" ./install.sh
```

## Demo in the README

The top of `README.md` has `demo:start` and `demo:end` markers. After reviewing
the recording for private information, upload the MP4 through GitHub's Markdown
editor and place the resulting attachment URL on its own line between them.
GitHub documents its supported formats and limits in
[Attaching files](https://docs.github.com/en/get-started/writing-on-github/working-with-advanced-formatting/attaching-files).
Keep the recording out of Git history; local video files are ignored.

Contributions are provided under the project's [MIT license](LICENSE).
