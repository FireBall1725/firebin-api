# Contributing to FireBin API

## Sign your commits (DCO)

Every commit in a pull request must carry a `Signed-off-by:` trailer certifying the [Developer Certificate of Origin 1.1](./DCO). It says you have the right to contribute the code and you're fine with it being distributed under the project's license.

To sign off, just pass `-s` to `git commit`:

```bash
git commit -s -m "fix: handle an empty response"
```

That appends a line like this to the commit message, using your `user.name` and `user.email` from git config:

```
Signed-off-by: Jane Contributor <jane@example.com>
```

If you forget on one commit, amend it:

```bash
git commit --amend -s --no-edit
```

If you forget on several, rebase with `--signoff`:

```bash
git rebase --signoff main
```

The [DCO GitHub App](https://github.com/apps/dco) runs on every PR and blocks the merge if any commit is missing a sign-off.
