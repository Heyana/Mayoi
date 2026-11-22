package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"mayoi/internal/git"
	"mayoi/locales"
)

// settleScmWorktrees draws frames until the section listed the worktrees
// and read those beside the window.
func settleScmWorktrees(t *testing.T, hs *harness, ctx *gitContext) {
	t.Helper()
	deadline := time.Now().Add(settleTimeout)
	for {
		hs.settle()
		w := &ctx.scm.worktrees
		ready := w.loaded && !w.loading
		if ready {
			for _, tr := range w.trees {
				if tr.wt.Current || tr.wt.Prunable || !tr.local || tr.err != "" {
					continue
				}
				if !tr.loaded || tr.reading {
					ready = false
				}
			}
		}
		if ready {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the worktrees never settled: loading %v loaded %v trees %d",
				w.loading, w.loaded, len(w.trees))
		}
	}
}

// scmWorktreeFixture is the harness's repository with a linked worktree
// named side, the source control view showing, and its status read.
func scmWorktreeFixture(t *testing.T) (*harness, func(args ...string) string, string) {
	t.Helper()
	hs := newHarness(t, true)
	run := hs.gitRepo()
	side := filepath.Join(t.TempDir(), "side")
	run("worktree", "add", "-b", "side", side)
	hs.a.run("view.scm")
	hs.settleGit()
	return hs, run, side
}

// openScmWorktrees opens the section, reading the worktrees again once
// it shows, and waits for them.
func openScmWorktrees(t *testing.T, hs *harness) *gitContext {
	t.Helper()
	ctx := hs.a.rootRepository()
	hs.a.settings.ScmWorktreesOpen = true
	ctx.invalidateScmWorktrees()
	hs.settleGit()
	hs.settle()
	settleScmWorktrees(t, hs, ctx)
	return ctx
}

// findScmWorktreeRow finds a row of the section by key.
func findScmWorktreeRow(t *testing.T, ctx *gitContext, key string) scmRow {
	t.Helper()
	for _, r := range ctx.scmRows() {
		if r.key == key {
			return r
		}
	}
	t.Fatalf("missing row %q of the worktrees section", key)
	return scmRow{}
}

// findScmWorktreeRowOf finds the row of a worktree whose folder is root.
func findScmWorktreeRowOf(t *testing.T, ctx *gitContext, root string) scmRow {
	t.Helper()
	for _, r := range ctx.scmRows() {
		if r.wt != nil && r.wt.kind == "tree" && r.wt.tree != nil && sameWorktreePath(r.wt.tree.wt.Path, root) {
			return r
		}
	}
	t.Fatalf("the section does not list the worktree %s", root)
	return scmRow{}
}

// The section follows the repository panel, lists every worktree with its
// changes, and folds and opens them.
func TestScmWorktreesSection(t *testing.T) {
	hs, _, side := scmWorktreeFixture(t)
	if err := os.WriteFile(filepath.Join(side, "side.go"), []byte("package side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := openScmWorktrees(t, hs)
	rows := ctx.scmRows()
	panelAt, headAt := -1, -1
	for i, r := range rows {
		switch r.key {
		case "panel:tabs":
			panelAt = i
		case "worktrees:header":
			headAt = i
		}
	}
	if panelAt < 0 || headAt <= panelAt {
		t.Fatalf("the section must follow the repository panel: panel %d header %d", panelAt, headAt)
	}
	if count := rows[headAt].count; count != 1 {
		t.Fatalf("header counts %d changed files, want 1", count)
	}
	if stat := rows[headAt].wt.stat; stat.Added != 1 || stat.Deleted != 0 {
		t.Fatalf("header counts +%d −%d lines, want +1 −0", stat.Added, stat.Deleted)
	}
	if !hs.tt.HasText(strings.ToUpper(locales.ScmWorktrees())) {
		t.Fatalf("the section has no title: %q", hs.tt.Texts())
	}
	w := &ctx.scm.worktrees
	if len(w.trees) != 2 || !w.trees[0].wt.Current {
		t.Fatalf("worktrees %d, first current %v", len(w.trees), w.trees[0].wt.Current)
	}
	tree := findScmWorktreeRowOf(t, ctx, side)
	if tree.wt.count != 1 || !tree.wt.known {
		t.Fatalf("side counts %d files, known %v", tree.wt.count, tree.wt.known)
	}
	if stat := tree.wt.stat; stat.Added != 1 || stat.Deleted != 0 {
		t.Fatalf("side counts +%d −%d lines, want +1 −0", stat.Added, stat.Deleted)
	}
	// A worktree starts folded, showing its counts rather than its files.
	if !w.closed[tree.key] {
		t.Fatal("the worktree starts open")
	}
	ctx.activateScmWorktreeRow(tree)
	hs.settle()
	file := findScmWorktreeRow(t, ctx, "worktree:"+tree.wt.tree.wt.Path+":"+fmt.Sprint(int(groupUntracked))+":"+filepath.Join(side, "side.go"))
	if file.wt.kind != "file" || !file.wt.e.Untracked() {
		t.Fatalf("file row %+v", file.wt)
	}
	// The worktree folds, keeping what was read of it.
	ctx.activateScmWorktreeRow(tree)
	hs.settle()
	if !w.closed[tree.key] {
		t.Fatal("the worktree did not fold")
	}
	for _, r := range ctx.scmRows() {
		if r.wt != nil && r.wt.kind == "file" && r.wt.tree == tree.wt.tree {
			t.Fatal("a folded worktree still shows its files")
		}
	}
	ctx.activateScmWorktreeRow(tree)
	hs.settle()
	if w.closed[tree.key] {
		t.Fatal("the worktree did not open again")
	}
	if !hs.tt.HasText("side.go") {
		t.Fatal("the opened worktree does not show its file")
	}
}

// The section starts open with its worktrees folded. Folded itself, it
// lists no worktree rows but still reads them, so its title counts them and
// opening it shows what was read.
func TestScmWorktreesStartsOpenAndReadsFolded(t *testing.T) {
	hs, _, side := scmWorktreeFixture(t)
	ctx := hs.a.rootRepository()
	w := &ctx.scm.worktrees
	if !hs.a.settings.ScmWorktreesOpen {
		t.Fatal("the section starts folded")
	}
	hs.settle()
	settleScmWorktrees(t, hs, ctx)
	if len(w.trees) != 2 || w.loading || !w.loaded {
		t.Fatalf("the open section read the worktrees: %+v", w)
	}
	if tr := findScmWorktreeRowOf(t, ctx, side); !w.closed[tr.key] || !tr.wt.known {
		t.Fatalf("the worktree does not start folded with its counts: %+v", tr.wt)
	}
	// A click on the header folds it, and its counts stay.
	if err := hs.tt.Click(strings.ToUpper(locales.ScmWorktrees())); err != nil {
		t.Fatal(err)
	}
	hs.settle()
	if hs.a.settings.ScmWorktreesOpen {
		t.Fatal("the section did not fold")
	}
	for _, r := range ctx.scmRows() {
		if r.wt != nil && r.wt.kind == "tree" {
			t.Fatal("a folded section lists worktrees")
		}
	}
	if !hs.tt.HasText(strings.ToUpper(locales.ScmWorktrees())) {
		t.Fatalf("a folded section has no title: %q", hs.tt.Texts())
	}
	if len(w.trees) != 2 || w.loading || !w.loaded {
		t.Fatalf("a folded section forgot the worktrees: %+v", w)
	}
	if err := hs.tt.Click(strings.ToUpper(locales.ScmWorktrees())); err != nil {
		t.Fatal(err)
	}
	hs.settleGit()
	hs.settle()
	if !hs.a.settings.ScmWorktreesOpen {
		t.Fatal("the section did not open")
	}
	if tr := w.treeAt(side); tr == nil || !tr.loaded {
		t.Fatalf("the linked worktree was not read: %+v", tr)
	}
	if tr := findScmWorktreeRowOf(t, ctx, side); tr.wt.kind != "tree" {
		t.Fatalf("the opened section does not list the worktree: %+v", tr.wt)
	}
}

// A file of a linked worktree opens a diff of that worktree, which reads
// it without joining the repositories the window lists.
func TestScmWorktreesDiffOfLinkedWorktree(t *testing.T) {
	hs, _, side := scmWorktreeFixture(t)
	if err := os.WriteFile(filepath.Join(side, "main.go"), []byte("package main // side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := openScmWorktrees(t, hs)
	tree := findScmWorktreeRowOf(t, ctx, side)
	if !ctx.scm.worktrees.closed[tree.key] {
		t.Fatal("the worktree starts open")
	}
	ctx.activateScmWorktreeRow(tree)
	hs.settle()
	file := findScmWorktreeRow(t, ctx, "worktree:"+tree.wt.tree.wt.Path+":"+fmt.Sprint(int(groupChanges))+":"+filepath.Join(side, "main.go"))
	a := hs.a
	a.repository().activateScmWorktreeRow(file)
	hs.settle()
	v := a.activeTab().diff
	if v == nil {
		t.Fatal("the file did not open a diff")
	}
	want, err := filepath.EvalSymlinks(side)
	if err != nil {
		t.Fatal(err)
	}
	if v.repoRoot != side && !sameWorktreePath(v.repoRoot, want) {
		t.Fatalf("diff of %q, want %q", v.repoRoot, side)
	}
	hs.settleDiff(v)
	if len(v.files) != 1 || v.files[0].path != file.wt.e.Path {
		t.Fatalf("diff lists %d files", len(v.files))
	}
	if !v.files[0].readOnly {
		t.Fatal("a file of a linked worktree is not read only")
	}
	// It is read here: its header opens it, and offers nothing that would
	// change it, as changing it would change a repository of its own.
	if v.files[0].writable() {
		t.Fatal("a file of a linked worktree is writable")
	}
	if _, ok := hs.tt.Find(locales.OpenFile()); !ok {
		t.Fatalf("the file of a linked worktree cannot be opened: %q", hs.tt.Texts())
	}
	for _, label := range []string{locales.StageChanges(), locales.UnstageChanges(), locales.DiscardChangesEllipsis(), locales.DiffRestoreFile()} {
		if _, ok := hs.tt.Find(label); ok {
			t.Fatalf("a file of a linked worktree offers %q", label)
		}
	}
	if c := a.diffContext(v); c.git.repo == nil || c.git.status == nil {
		t.Fatalf("the linked worktree's diff has no repository: %+v", c.git)
	}
	if a.repositoryForRoot(side).git.repo != nil {
		t.Fatal("the linked worktree joined the window's repositories")
	}
	if len(a.repositories.contexts) != 1 {
		t.Fatalf("%d repositories, want the window's own", len(a.repositories.contexts))
	}
	if n := a.changeCount(); n != 0 {
		t.Fatalf("the window counts %d changes of another worktree", n)
	}
}

// A worktree whose folder is gone shows as missing, without files, and
// its menu leads to pruning it.
func TestScmWorktreesMissingWorktree(t *testing.T) {
	hs, run, _ := scmWorktreeFixture(t)
	gone := filepath.Join(t.TempDir(), "gone")
	run("worktree", "add", "-b", "gone", gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	ctx := openScmWorktrees(t, hs)
	tree := findScmWorktreeRowOf(t, ctx, gone)
	if !tree.wt.tree.wt.Prunable {
		t.Fatalf("a worktree whose folder is gone is not prunable: %+v", tree.wt.tree.wt)
	}
	if !hs.tt.HasText(locales.WorktreeMissingHint()) {
		t.Fatalf("the missing worktree is not said: %q", hs.tt.Texts())
	}
	// Its folder was not read: the worktrees it is not among are not said
	// to be all clean.
	if hs.tt.HasText(locales.ScmWorktreesClean()) {
		t.Fatalf("a worktree whose folder is gone is called clean: %q", hs.tt.Texts())
	}
	for _, r := range ctx.scmRows() {
		if r.wt != nil && r.wt.kind == "file" && r.wt.tree == tree.wt.tree {
			t.Fatal("a missing worktree lists files")
		}
	}
	if err := hs.tt.RightClick(filepath.Base(gone)); err != nil {
		t.Fatal(err)
	}
	menu := hs.tt.Menu()
	if !slicesContain(menu, locales.ScmWorktreeOpen()) || !slicesContain(menu, locales.ScmWorktreePrune()) ||
		!slicesContain(menu, locales.CopyPath()) {
		t.Fatalf("menu of a missing worktree: %q", menu)
	}
	hs.tt.Key(0, ui.KeyEscape)
	hs.settle()
	// Choosing it offers to prune it rather than opening it.
	ctx.askOpenWorktree(tree.wt.tree.wt.Branch, gone)
	hs.settle()
	if hs.a.confirm == nil {
		t.Fatal("opening a missing worktree asks nothing")
	}
	if got := hs.a.confirm.title; got != locales.WorktreeMissingTitle() {
		t.Fatalf("opening a missing worktree asks %q", got)
	}
	hs.tt.Key(0, ui.KeyEscape)
	hs.settle()
}

// Every other worktree clean says so, and one worktree leaves no section to
// count the changes above it twice.
func TestScmWorktreesCleanAndNone(t *testing.T) {
	hs, run, side := scmWorktreeFixture(t)
	ctx := openScmWorktrees(t, hs)
	if findScmWorktreeRow(t, ctx, "worktrees:header").count != 0 {
		t.Fatal("a clean repository counts changes")
	}
	if !hs.tt.HasText(locales.ScmWorktreesClean()) {
		t.Fatalf("all worktrees clean is not said: %q", hs.tt.Texts())
	}
	run("worktree", "remove", "--force", side)
	ctx.invalidateScmWorktrees()
	hs.settleGit()
	hs.settle()
	settleScmWorktrees(t, hs, ctx)
	if len(ctx.scm.worktrees.trees) != 1 {
		t.Fatalf("worktrees %d after removing the linked one", len(ctx.scm.worktrees.trees))
	}
	for _, r := range ctx.scmRows() {
		if r.key == "worktrees:header" {
			t.Fatal("the section still counts the worktree above it")
		}
	}
}

// A worktree removed with git leaves the section on its own, without a
// refresh asked for: its row goes, and with no other worktree the whole
// section goes, rather than counting the changes above it.
func TestScmWorktreesFollowsRemoval(t *testing.T) {
	hs, run, side := scmWorktreeFixture(t)
	if err := os.WriteFile(filepath.Join(side, "side.go"), []byte("package side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := openScmWorktrees(t, hs)
	if findScmWorktreeRow(t, ctx, "worktrees:header").count != 1 {
		t.Fatal("the section does not count the linked worktree's file")
	}
	// Folded, the section still follows the worktrees.
	if err := hs.tt.Click(strings.ToUpper(locales.ScmWorktrees())); err != nil {
		t.Fatal(err)
	}
	hs.settle()
	if hs.a.settings.ScmWorktreesOpen {
		t.Fatal("the section did not fold")
	}
	run("worktree", "remove", "--force", side)
	hs.settleGit()
	w := &ctx.scm.worktrees
	deadline := time.Now().Add(settleTimeout)
	for {
		hs.settle()
		if len(w.trees) == 1 && !w.loading && w.loaded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the removed worktree is still listed: %d trees, loading %v", len(w.trees), w.loading)
		}
	}
	for _, r := range ctx.scmRows() {
		if r.key == "worktrees:header" {
			t.Fatal("the section still counts the worktree above it")
		}
		if r.wt != nil && r.wt.kind == "tree" && r.wt.tree != nil && sameWorktreePath(r.wt.tree.wt.Path, side) {
			t.Fatal("the removed worktree still has a row")
		}
	}
}

// slicesContain is slices.Contains, for the menu of the section.
func slicesContain(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// A worktree whose folder is gone is never read: alone beside the window's,
// it does not let the section say that the worktrees are clean.
func TestScmWorktreesMissingWorktreeIsNotClean(t *testing.T) {
	hs := newHarness(t, true)
	run := hs.gitRepo()
	gone := filepath.Join(t.TempDir(), "gone")
	run("worktree", "add", "-b", "gone", gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	hs.a.run("view.scm")
	hs.settleGit()
	ctx := openScmWorktrees(t, hs)
	tree := findScmWorktreeRowOf(t, ctx, gone)
	if !tree.wt.tree.wt.Prunable {
		t.Fatalf("a worktree whose folder is gone is not prunable: %+v", tree.wt.tree.wt)
	}
	if hs.tt.HasText(locales.ScmWorktreesClean()) {
		t.Fatalf("a worktree whose folder is gone is called clean: %q", hs.tt.Texts())
	}
}

// The files of a worktree show the folder they are in, as the files of the
// window's own work tree do, to tell files of one name apart; and the tip of
// a row says that a click shows its changes, which the buttons open it.
func TestScmWorktreesFileRowShowsFolderAndTip(t *testing.T) {
	hs, _, side := scmWorktreeFixture(t)
	for _, dir := range []string{"one", "two"} {
		if err := os.MkdirAll(filepath.Join(side, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(side, dir, "same.go"), []byte("package "+dir+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := openScmWorktrees(t, hs)
	ctx.activateScmWorktreeRow(findScmWorktreeRowOf(t, ctx, side))
	hs.settle()
	if !hs.tt.HasText("one") || !hs.tt.HasText("two") {
		t.Fatalf("the files of a worktree do not show their folders: %q", hs.tt.Texts())
	}
	area, ok := hs.tt.Find("same.go")
	if !ok {
		t.Fatalf("the files of a worktree are not shown: %q", hs.tt.Texts())
	}
	hs.tt.Move(area.X+area.W/2, area.Y+area.H/2)
	hs.tt.Advance(900 * time.Millisecond)
	hs.tt.Frame()
	if !hs.tt.HasText(locales.ScmWorktreeShowChanges()) {
		t.Fatalf("the tip of a file says %q", hs.tt.Texts())
	}
}

// Left on the row of a file goes back up to the worktree it belongs to, as
// it does on a file of a commit.
func TestScmWorktreesLeftGoesBackToTheWorktree(t *testing.T) {
	hs, _, side := scmWorktreeFixture(t)
	if err := os.WriteFile(filepath.Join(side, "side.go"), []byte("package side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := openScmWorktrees(t, hs)
	// The header has the keyboard's list: a click on it folds and opens the
	// section, which opens the worktree's files.
	if err := hs.tt.Click(strings.ToUpper(locales.ScmWorktrees())); err != nil {
		t.Fatal(err)
	}
	hs.settle()
	if err := hs.tt.Click(strings.ToUpper(locales.ScmWorktrees())); err != nil {
		t.Fatal(err)
	}
	settleScmWorktrees(t, hs, ctx)
	// The read lists the worktrees again, which open the worktree's files.
	tree := findScmWorktreeRowOf(t, ctx, side)
	if ctx.scm.worktrees.closed[tree.key] {
		ctx.activateScmWorktreeRow(tree)
		hs.settle()
	}
	fileAt, treeAt := -1, -1
	for i, r := range ctx.scmRows() {
		switch {
		case r.key == tree.key:
			treeAt = i
		case r.wt != nil && r.wt.kind == "file" && r.wt.tree != nil && sameWorktreePath(r.wt.tree.wt.Path, side):
			fileAt = i
		}
	}
	if fileAt < 0 || treeAt < 0 {
		t.Fatalf("the section lists no file of the worktree: file %d, worktree %d, folded %v", fileAt, treeAt,
			ctx.scm.worktrees.closed[tree.key])
	}
	ctx.scm.row = fileAt
	hs.tt.Key(0, ui.KeyLeft)
	hs.settle()
	if ctx.scm.row != treeAt {
		t.Fatalf("Left left the row on %d of the section, not the worktree %d", ctx.scm.row, treeAt)
	}
}

// The changes of a worktree beside the window's are read again every
// worktreeStatusEvery while the section shows, or while a diff of one
// shows, and not while it is folded and unread.
func TestScmWorktreesReadsChangesAgainWhileShown(t *testing.T) {
	hs, _, side := scmWorktreeFixture(t)
	if err := os.WriteFile(filepath.Join(side, "side.go"), []byte("package side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := openScmWorktrees(t, hs)
	w := &ctx.scm.worktrees
	if n := findScmWorktreeRowOf(t, ctx, side).wt.count; n != 1 {
		t.Fatalf("the worktree counts %d files, want 1", n)
	}
	// A file changed in it, folded and unread: the section waits.
	if err := os.WriteFile(filepath.Join(side, "later.go"), []byte("package side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := hs.tt.Click(strings.ToUpper(locales.ScmWorktrees())); err != nil {
		t.Fatal(err)
	}
	hs.settle()
	if hs.a.settings.ScmWorktreesOpen {
		t.Fatal("the section did not fold")
	}
	read := w.read
	hs.tt.Advance(worktreeStatusEvery + time.Second)
	hs.settle()
	if !w.read.Equal(read) {
		t.Fatal("a folded section read the worktrees again")
	}
	if tr := w.treeAt(side); tr == nil || tr.fileCount != 1 {
		t.Fatalf("a folded section counts %+v, want 1 file", tr)
	}
	// Shown, it reads them again every worktreeStatusEvery.
	if err := hs.tt.Click(strings.ToUpper(locales.ScmWorktrees())); err != nil {
		t.Fatal(err)
	}
	settleScmWorktrees(t, hs, ctx)
	if tr := w.treeAt(side); tr == nil || tr.fileCount != 2 {
		t.Fatalf("opening the section counts %+v, want 2 files", tr)
	}
	// A file changed while it shows, and the section reads it again of its
	// own: no click, no worktree made or removed.
	if err := os.WriteFile(filepath.Join(side, "third.go"), []byte("package side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hs.tt.Advance(worktreeStatusEvery + time.Second)
	settleScmWorktrees(t, hs, ctx)
	if tr := w.treeAt(side); tr == nil || tr.fileCount != 3 {
		t.Fatalf("the open section counts %+v after %s, want 3 files", tr, worktreeStatusEvery)
	}
}

// A diff of a worktree beside the window's shows its changes as they are
// made, though the section beside it is folded: the read goes on while the
// tab shows, and the diff lists the file added without opening it again.
func TestScmWorktreesOutsideDiffReadsChangesAgain(t *testing.T) {
	hs, _, side := scmWorktreeFixture(t)
	if err := os.WriteFile(filepath.Join(side, "main.go"), []byte("package main // side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := openScmWorktrees(t, hs)
	tree := findScmWorktreeRowOf(t, ctx, side)
	ctx.activateScmWorktreeRow(tree)
	hs.settle()
	file := findScmWorktreeRow(t, ctx, "worktree:"+tree.wt.tree.wt.Path+":"+fmt.Sprint(int(groupChanges))+":"+filepath.Join(side, "main.go"))
	hs.a.rootRepository().activateScmWorktreeRow(file)
	hs.settle()
	v := hs.a.activeTab().diff
	hs.settleDiff(v)
	if len(v.files) != 1 {
		t.Fatalf("the diff lists %d files, want 1", len(v.files))
	}
	// Folded, with the diff showing, the changes are read all the same.
	if err := hs.tt.Click(strings.ToUpper(locales.ScmWorktrees())); err != nil {
		t.Fatal(err)
	}
	hs.settle()
	if hs.a.settings.ScmWorktreesOpen {
		t.Fatal("the section did not fold")
	}
	if err := os.WriteFile(filepath.Join(side, "added.go"), []byte("package side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hs.tt.Advance(worktreeStatusEvery + time.Second)
	hs.settle()
	if n := len(v.files); n != 2 {
		t.Fatalf("the diff lists %d files after %s, want 2", n, worktreeStatusEvery)
	}
	if tr := ctx.scm.worktrees.treeAt(side); tr == nil || tr.fileCount != 2 {
		t.Fatalf("the section counts %+v after %s, want 2 files", tr, worktreeStatusEvery)
	}
	// The diff of the worktree reads what was read of it again.
	if c := hs.a.worktreeGit(side); c == nil || c.git.status == nil || len(c.git.status.Entries) != 2 {
		t.Fatalf("the diff of the worktree reads %+v, want its 2 files", c)
	}
}

// A refresh keeps what was read of the worktrees it lists again, rather than
// dropping it to read every count again: the rows keep their counts, and do
// not fall back to "…" every refresh.
func TestScmWorktreesKeepsWhatWasReadOnRefresh(t *testing.T) {
	hs, _, side := scmWorktreeFixture(t)
	ctx := openScmWorktrees(t, hs)
	read := ctx.scm.worktrees.treeAt(side)
	if read == nil || read.status == nil {
		t.Fatalf("the worktree of the section was not read: %+v", read)
	}
	w := &scmWorktrees{closed: map[string]bool{}}
	w.trees = []*scmWorktree{{wt: read.wt, local: true, status: read.status, fileCount: 3, loaded: true}}
	ctx.listScmWorktrees(w, []git.Worktree{read.wt}, false)
	if len(w.trees) != 1 {
		t.Fatalf("the section lists %d worktrees, want 1", len(w.trees))
	}
	next := w.trees[0]
	if !next.known() {
		t.Fatal("a worktree read before is unknown after the refresh")
	}
	if next.status != read.status || next.fileCount != 3 {
		t.Fatalf("the refresh dropped what was read: known %v, count %d", next.known(), next.fileCount)
	}
}

// The tab of a worktree's changes says whose it is, to tell the tabs of the
// worktrees apart, while the window's own changes keep their name.
func TestScmWorktreesDiffTabNamesTheWorktree(t *testing.T) {
	hs, _, side := scmWorktreeFixture(t)
	if err := os.WriteFile(filepath.Join(side, "main.go"), []byte("package main // side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	openScmWorktrees(t, hs)
	a := hs.a
	a.showChanges(diffTarget{base: diffIndex}, true, side)
	hs.settle()
	vd := a.activeTab().diff
	if vd.repoRoot != side && !sameWorktreePath(vd.repoRoot, side) {
		t.Fatalf("the changes opened are those of %q", vd.repoRoot)
	}
	if len(vd.files) != 1 {
		t.Fatalf("the changes of the worktree list %d files, want 1", len(vd.files))
	}
	hs.settleDiff(vd)
	if got, want := a.activeTab().name(), locales.DiffChangesOfWorktree(filepath.Base(side)); got != want {
		t.Fatalf("the changes of a worktree are named %q, want %q", got, want)
	}
	a.showChanges(diffTarget{base: diffIndex}, true)
	hs.settle()
	if got, want := a.activeTab().name(), locales.DiffChangesName(); got != want {
		t.Fatalf("the window's changes are named %q, want %q", got, want)
	}
}
