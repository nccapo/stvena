# Security

Stvena is early-stage software. Security fixes target the latest code on `main`;
there are no maintained older release lines yet.

## Report a vulnerability

Use [GitHub private vulnerability reporting](https://github.com/nccapo/stvena/security/advisories/new)
when available. If it is unavailable, open an issue requesting a private contact
without including exploit details, credentials, or private source code.

Include the affected commit, reproduction steps using synthetic data, likely
impact, and relevant OS and terminal details. Never post a live credential.

## Trust boundaries

- Agent processes inherit your environment and run with your permissions.
- Check commands run through `sh` with your permissions and environment in a
  temporary checkout. They are not sandboxed; Git filters can also execute.
- Snapshots retain nonignored source and tracked files in local Git objects.
  Review data and check logs may contain source, paths, and sensitive output.
- Ignoring a file does not remove it from prior captures or Git history.
- Clipboard and agent-paste actions intentionally expose the selected content
  to their destination. Inspect drafts before submitting them.

If credentials are exposed, revoke or rotate them. Deleting a file in a later
commit does not remove it from Git history or from previously shared copies.
