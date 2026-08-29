// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

// Package scripttest exercises the release-control shell scripts with
// ephemeral fixtures, so their discriminating behavior is enforced by CI
// rather than asserted in prose: the tag-signature verifier must accept
// exactly the approved keyring and honor GnuPG's own rejection, and the
// latest-release selector must prefer a stable release over a higher
// prerelease.
package scripttest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func requireUnixTools(t *testing.T, tools ...string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixtures run on the unix CI legs")
	}
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available: %v", tool, err)
		}
	}
}

type signLab struct {
	t       *testing.T
	repo    string
	gnupg   string
	scripts string
}

func (l *signLab) git(args ...string) string {
	l.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", l.repo}, args...)...)
	cmd.Env = append(os.Environ(),
		"GNUPGHOME="+l.gnupg,
		// The lab must not inherit the developer's or runner's git
		// configuration (signing keys, gpgsign defaults).
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.invalid",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		l.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (l *signLab) gpg(args ...string) string {
	l.t.Helper()
	cmd := exec.Command("gpg", args...)
	cmd.Env = append(os.Environ(), "GNUPGHOME="+l.gnupg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		l.t.Fatalf("gpg %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (l *signLab) genKey(uid string) string {
	l.t.Helper()
	l.gpg("--batch", "--pinentry-mode", "loopback", "--passphrase", "",
		"--quick-gen-key", uid, "ed25519", "sign", "1d")
	cols := l.gpg("--with-colons", "--list-secret-keys", uid)
	for _, line := range strings.Split(cols, "\n") {
		if strings.HasPrefix(line, "fpr:") {
			return strings.Split(line, ":")[9]
		}
	}
	l.t.Fatalf("no fingerprint for %s", uid)
	return ""
}

func (l *signLab) exportKeyring(path string, fprs ...string) {
	l.t.Helper()
	var buf strings.Builder
	for _, f := range fprs {
		buf.WriteString(l.gpg("--armor", "--export", f))
	}
	if err := os.WriteFile(path, []byte(buf.String()), 0o644); err != nil {
		l.t.Fatal(err)
	}
}

// verify runs the production verifier inside the lab repo and returns
// (accepted, combined output).
func (l *signLab) verify(tag, keyring string) (bool, string) {
	l.t.Helper()
	cmd := exec.Command("sh", filepath.Join(l.scripts, "verify-release-tag.sh"), tag, keyring)
	cmd.Dir = l.repo
	out, err := cmd.CombinedOutput()
	return err == nil, string(out)
}

func newSignLab(t *testing.T) *signLab {
	requireUnixTools(t, "gpg", "git", "sh")
	// GnuPG needs a SHORT home: its agent socket path must fit the unix
	// socket limit, which t.TempDir()'s long names can exceed.
	gnupg, err := os.MkdirTemp("/tmp", "gpg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(gnupg) })
	l := &signLab{t: t, repo: t.TempDir(), gnupg: gnupg, scripts: filepath.Join(repoRoot(t), "scripts")}
	if err := os.Chmod(l.gnupg, 0o700); err != nil {
		t.Fatal(err)
	}
	l.git("init", "-q")
	if err := os.WriteFile(filepath.Join(l.repo, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	l.git("add", "f")
	l.git("commit", "-q", "-m", "c")
	return l
}

func TestVerifierAcceptsOnlyTheApprovedKeyring(t *testing.T) {
	l := newSignLab(t)
	current := l.genKey("current <c@t.invalid>")
	successor := l.genKey("successor <s@t.invalid>")
	unrelated := l.genKey("unrelated <u@t.invalid>")

	l.git("-c", "gpg.program=gpg", "tag", "-u", current, "-s", "signed-current", "-m", "m")
	l.git("-c", "gpg.program=gpg", "tag", "-u", successor, "-s", "signed-successor", "-m", "m")
	l.git("-c", "gpg.program=gpg", "tag", "-u", unrelated, "-s", "signed-unrelated", "-m", "m")
	l.git("tag", "-a", "unsigned", "-m", "m")

	kr := filepath.Join(l.repo, "keyring.asc")
	l.exportKeyring(kr, current)
	rotation := filepath.Join(l.repo, "rotation.asc")
	l.exportKeyring(rotation, current, successor)

	if ok, out := l.verify("signed-current", kr); !ok {
		t.Fatalf("current key rejected: %s", out)
	}
	if ok, out := l.verify("signed-successor", kr); ok {
		t.Fatalf("successor accepted before rotation: %s", out)
	}
	if ok, out := l.verify("signed-successor", rotation); !ok {
		t.Fatalf("successor rejected during rotation: %s", out)
	}
	if ok, out := l.verify("signed-unrelated", rotation); ok {
		t.Fatalf("unrelated key accepted: %s", out)
	}
	if ok, out := l.verify("unsigned", kr); ok {
		t.Fatalf("unsigned annotated tag accepted: %s", out)
	}
}

func TestVerifierHonorsGnuPGRejection(t *testing.T) {
	// A tag whose signature block is present but cryptographically invalid
	// must be rejected because git verify-tag itself fails — signer
	// identity text alone can never satisfy the gate.
	l := newSignLab(t)
	current := l.genKey("current <c@t.invalid>")
	kr := filepath.Join(l.repo, "keyring.asc")
	l.exportKeyring(kr, current)

	commit := strings.TrimSpace(l.git("rev-parse", "HEAD"))
	tagBody := fmt.Sprintf(`object %s
type commit
tag malformed
tagger t <t@t.invalid> 1700000000 +0000

m
-----BEGIN PGP SIGNATURE-----

aW52YWxpZA==
=zzzz
-----END PGP SIGNATURE-----
`, commit)
	mk := exec.Command("git", "-C", l.repo, "mktag")
	mk.Stdin = strings.NewReader(tagBody)
	sha, err := mk.Output()
	if err != nil {
		t.Skipf("mktag rejected the malformed fixture: %v", err)
	}
	l.git("update-ref", "refs/tags/malformed", strings.TrimSpace(string(sha)))
	if ok, out := l.verify("malformed", kr); ok {
		t.Fatalf("malformed signature accepted: %s", out)
	}
}

func TestLatestReleasePrefersStableOverHigherPrerelease(t *testing.T) {
	requireUnixTools(t, "sh", "go")
	proxy := t.TempDir()
	v := filepath.Join(proxy, "example.com", "probe", "@v")
	if err := os.MkdirAll(v, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"list":             "v0.1.9\nv0.2.0-rc.1\n",
		"v0.1.9.info":      `{"Version":"v0.1.9","Time":"2026-08-01T00:00:00Z"}`,
		"v0.2.0-rc.1.info": `{"Version":"v0.2.0-rc.1","Time":"2026-08-20T00:00:00Z"}`,
		"v0.1.9.mod":       "module example.com/probe\n\ngo 1.21\n",
		"v0.2.0-rc.1.mod":  "module example.com/probe\n\ngo 1.21\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(v, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "go.mod"), []byte("module probe\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", filepath.Join(repoRoot(t), "scripts", "latest-release.sh"), "example.com/probe")
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "GOPROXY=file://"+filepath.ToSlash(proxy), "GOSUMDB=off", "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("latest-release.sh: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "v0.1.9" {
		t.Fatalf("selected %q, want the stable v0.1.9 over the higher prerelease", got)
	}
}
