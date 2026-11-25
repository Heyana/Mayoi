package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	gitpkg "mayoi/internal/git"
	"mayoi/locales"
)

func TestPublishRepoFlow(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	prevGH := gitpkg.GH
	t.Cleanup(func() { gitpkg.GH = prevGH })

	var gotArgs []string
	gitpkg.GH = func(args ...string) ([]byte, error) {
		gotArgs = slices.Clone(args)
		return []byte("https://github.com/me/demo\n"), nil
	}

	hs := newHarness(t, true)
	hs.gitRepo()
	hs.a.showSourceControl()
	hs.settleGit()

	if _, ok := hs.tt.Find(locales.ScmPublishRepo()); !ok {
		t.Fatalf("publish repository button missing when no remotes exist: %q", hs.tt.Texts())
	}

	// Open the prompt via the repository action and verify defaults.
	hs.a.askPublishRepo()
	hs.settle()
	if hs.a.prompt == nil {
		t.Fatal("publish prompt did not open")
	}
	if want := filepath.Base(hs.root); hs.a.prompt.value != want {
		t.Fatalf("default repo name = %q, want %q", hs.a.prompt.value, want)
	}
	if !slices.Equal(hs.a.prompt.choices, []string{locales.ScmRepoPrivate(), locales.ScmRepoPublic()}) || hs.a.prompt.choice != 0 {
		t.Fatalf("visibility choices = %q (choice %d)", hs.a.prompt.choices, hs.a.prompt.choice)
	}

	// Reject an invalid name without closing the prompt.
	if msg := hs.a.prompt.done("bad name"); msg != locales.ScmPublishRepoBadName() {
		t.Fatalf("invalid name returned %q, want %q", msg, locales.ScmPublishRepoBadName())
	}
	if gotArgs != nil {
		t.Fatalf("gh called for invalid name: %q", gotArgs)
	}

	// Switch to Public and submit.
	hs.a.prompt.choice = 1
	if msg := hs.a.prompt.done("demo"); msg != "" {
		t.Fatalf("valid name returned error %q", msg)
	}
	hs.a.prompt = nil
	hs.settleGit()

	wantArgs := []string{"repo", "create", "demo", "--source=.", "--remote=origin", "--public", "--push"}
	if !slices.Equal(gotArgs, wantArgs) {
		t.Fatalf("gh args = %q, want %q", gotArgs, wantArgs)
	}
	if !hs.tt.HasText(locales.PublishedRepo("demo")) || !hs.tt.HasText(locales.PrOpenOnGitHub()) {
		t.Fatalf("missing success toast after publishing; UI: %q", hs.tt.Texts())
	}

	// Signed-out gh offers a sign-in dialog.
	gitpkg.GH = func(args ...string) ([]byte, error) {
		return nil, fmt.Errorf("%w: To get started with GitHub CLI, run: gh auth login", gitpkg.ErrNoGH)
	}
	hs.a.repository().publishRepo("demo", true)
	hs.settleGit()
	if hs.a.confirm == nil || hs.a.confirm.title != locales.GhSignedOut() || hs.a.confirm.ok != locales.GhSignIn() {
		t.Fatalf("signed-out confirm = %+v, want %q", hs.a.confirm, locales.GhSignedOut())
	}
	hs.a.confirm = nil

	// Missing gh offers the install dialog.
	gitpkg.GH = func(args ...string) ([]byte, error) {
		return nil, fmt.Errorf("%w: %w", gitpkg.ErrNoGH, exec.ErrNotFound)
	}
	hs.a.repository().publishRepo("demo", true)
	hs.settleGit()
	if hs.a.confirm == nil || hs.a.confirm.title != locales.GhMissing() || hs.a.confirm.ok != locales.GhGetCLI() {
		t.Fatalf("missing gh confirm = %+v, want %q", hs.a.confirm, locales.GhMissing())
	}
}

func TestInitRepoCommand(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	hs := newHarness(t, true)
	hs.a.showSourceControl()
	hs.settleGit()
	if hs.a.git.repo != nil {
		t.Fatal("expected non-git folder initially")
	}

	hs.a.initRepo()
	hs.settleGit()
	if hs.a.git.repo == nil {
		t.Fatal("initRepo did not initialize a repository")
	}

	// Calling initRepo again on the same root reports it is already a repo.
	before := len(hs.a.errors)
	hs.a.initRepo()
	if len(hs.a.errors) != before+1 || hs.a.errors[before] != locales.ScmAlreadyRepo() {
		t.Fatalf("errors = %q, want %q", hs.a.errors, locales.ScmAlreadyRepo())
	}
}
