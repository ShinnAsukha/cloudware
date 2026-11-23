# Cloudware GitHub Update Center — v0.1.0

**Private GitHub repository**: `ShinnAsukha/cloudware`.

## Implemented

- Independent Windows AMD64 updater (`CLOUDWARE-UPDATE-CENTER.exe`), built with Go 1.23.
- `check` queries *private* GitHub releases using the locally authenticated GitHub CLI, without modifying Cloudware.
- `download` only stages a selected `cw-app-X.Y.Z` release and refuses to proceed unless its artifact attestation verifies against `.github/workflows/cloudware-release.yml`, its embedded manifest validates, and the SHA256 of every file matches its declared value and the **current local source file** matches its expected-before SHA256.
- `install` requires interactive text confirmation `KUR cw-app-X.Y.Z`. It can apply **only file additions/replacements**, never remove files or execute a downloaded script. Database migrations are forbidden in this first stage.
- Backup, Docker compose build, restart and container running/healthy check. If any of these fail, it attempts source+service rollback and reports `FAIL`, including rollback errors. It cannot guarantee database/production recovery and is not a release readiness verdict.
- Setup copies the updater to `E:\Cloudware\tools\CloudwareUpdateCenter` and attempts to register a *check-only* 30-minute scheduled task. Check / Download / Approve-install batch launchers are created locally. No services or app sources are changed at bootstrap time.
- GitHub Actions for two rounds of Go tests (Linux/race and Windows), candidate builder negative tests, and manually dispatched, attested, stable releases.

## Requirements

- GitHub CLI (`gh`) installed, signed in with `gh auth login` **on this Windows user account**, and authorized to access private `ShinnAsukha/cloudware`.
- A real `E:\Cloudware` folder with a Compose file and running Docker Desktop *when applying updates*.
- Private repo and availability of GitHub artifact attestations (requires an eligible GitHub Enterprise Cloud plan for private repositories). If verification is unsupported or fails, installation is deliberately blocked.
- No production release is published yet; an existing demo README is not the Cloudware application source. `check` will report `no approved application release` until an actual release candidate is integrated, tested, approved and published.

## First-time setup

1. Download the one-time bootstrap ZIP, extract, double-click `KUR.bat`. No copy/paste.
2. If GitHub CLI is missing, install it from https://cli.github.com/. Run `gh auth login` once in the Windows terminal using your own account (never paste your token into chat).
3. Afterwards use `E:\Cloudware\tools\CloudwareUpdateCenter\CHECK.bat` to check, `DOWNLOAD.bat` to validate a future release, and `ONAYLA-VE-KUR.bat` to explicitly approve installing a future **tested** release. The scheduled task, if registered, only checks; it never installs.

## Next development before first application release

The repo currently contains just its README, not the live Cloudware API/Web/DB source. Before publishing `cw-app-*` releases, import the actual source from `E:\Cloudware` after excluding secrets, configure branch protections/production environment approval, integrate end-to-end tests and verify Windows build + Docker against the true repo. **Do not invent a candidate from old ZIPs**.

### Security limitations

- Repository administrators and GitHub Actions release workflow maintainers form the release trust boundary; protect them with 2FA, least privilege and main branch rules.
- Updater's GitHub CLI credentials are managed by GitHub CLI, not copied into this package. Never publish private repo contents or use unauthenticated public downloads.
- Automatic check does not mean automatic installation. Database migrations and public traffic cutover are not supported in this stage.
- Before any real deployment, rehearse backup + rollback on a representative system and confirm APIs, UI, DNS, CDN, mail and security separately. Current outstanding Phase 4B/5A release-blocking findings remain open.

## One-time source import to GitHub

After setup, double-click `GITHUB-AKTAR.bat` from this extracted bootstrap package. It uses your existing local `gh` and `git` login, requires explicit `YUKLE` confirmation and pushes **only updater files** to `feature/github-update-center-v1`, opening a draft PR for code review. No Cloudware application source or credentials are copied. GitHub Actions become active after the PR is merged into main and passes review.

**Private repository plan restriction:** GitHub artifact attestations in private repositories require eligible GitHub Enterprise Cloud plans. On unsupported plans, publishing/verification fails closed. This is deliberate; until a supported, separately reviewed signing process exists, do not publish a real application update. GitHub connection and check-only bootstrap can still work. Environment required-reviewer protection for private repos also varies by account plan; do not assume it is configured.