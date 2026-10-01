# Upgrading a Project

Every FGOTHS project carries its own copy of the runtime in `pkg/runtime/`.
That's deliberate: installing a newer CLI never changes a running project
behind your back. `fgoths upgrade` brings the runtime up to date **when you
decide to**, and it keeps your local edits.

---

## The short version

```bash
go install github.com/WhoseBiasDoYallSeek/fgoths-framework/cmd/fgoths@latest   # get the latest CLI

fgoths upgrade --dir=my-service           # 1. preview
fgoths upgrade --dir=my-service --apply   # 2. apply

cd my-service && go test -race ./...      # 3. verify
```

Nothing is written until you pass `--apply`.

---

## Which version is my project on?

| Project created with | What to run |
|---|---|
| **v1.3.0 or later** | Nothing extra. The version is recorded in `.fgoths/upgrade.json`. |
| **v1.2.0** | Add `--from=1.2.0` |
| **v1.1.0** | Add `--from=1.1.0` |
| Older | Not supported. Regenerate the project and move your handlers over. |

```bash
fgoths upgrade --from=1.1.0 --dir=my-site
```

If you pass a version FGOTHS doesn't know, it stops instead of guessing.

---

## Reading the preview

```
FGOTHS runtime upgrade plan: v1.2.0 -> v1.4.0
  Update pkg/runtime/server.go
  Merge local changes in pkg/runtime/proxy.go
  Conflict in pkg/runtime/metrics.go; original will be preserved
  Current pkg/runtime/auth.go
Dry run only. Re-run with --apply to write safe updates.
```

| Line | Meaning |
|---|---|
| **Update** | You never edited this file. It is replaced with the new version. |
| **Merge local changes** | You edited this file, and your edits combine cleanly with the new version. |
| **Conflict** | Your edits overlap with the update. **Your file is left untouched**, and FGOTHS writes a merged proposal for you to review. |
| **Current** | Already up to date. |

---

## After `--apply`

Your project gains a few things under `.fgoths/`:

| Path | What it is | Commit it? |
|---|---|---|
| `.fgoths/upgrade.json` | The recorded version and file fingerprints | ✅ Yes |
| `.fgoths/upgrade-backups/` | A copy of every file before it changed | ❌ No (ignored) |
| `.fgoths/upgrade-conflicts/` | Proposed merges for conflicting files | ❌ No (ignored) |
| `.fgoths/.gitignore` | Keeps backups and conflicts out of Git | ✅ Yes |

### Resolving a conflict

1. Open the proposal in `.fgoths/upgrade-conflicts/`. It contains standard
   `<<<<<<<` / `>>>>>>>` markers.
2. Copy the resolved version over the original file in `pkg/runtime/`.
3. Run `go test -race ./...`.

To roll back, copy a file back from `.fgoths/upgrade-backups/`, or use
`git checkout`.

---

## What upgrade does *not* touch

`upgrade` only updates the managed runtime files: `server.go`, `router.go`,
`proxy.go`, `metrics.go`, `hmr/hmr.go`, and `auth.go` (when present) under
`pkg/runtime/`. It never
regenerates your handlers, views, models, migrations, routes, `main.go`, or
configuration. That code is yours.

If a release adds a new capability to generated code, such as a new
middleware in `main.go`, the [CHANGELOG](../CHANGELOG.md) explains how to
adopt it by hand.

---

## Checklist

- [ ] Commit or stash your work first, so the upgrade diff is easy to review.
- [ ] Make sure Git is installed. It is required when runtime files have
      local edits.
- [ ] Use a CLI built from the release you are upgrading **to**. The CLI
      refuses to run if its version doesn't match the target runtime.
- [ ] After applying: `go test -race ./...`, `make build`, then your usual
      deploy checks.
- [ ] Commit `pkg/runtime/` and `.fgoths/upgrade.json` together.
