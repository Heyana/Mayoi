package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"mayoi/internal/bg"
	"mayoi/internal/git"
	"mayoi/internal/remote"
	"mayoi/locales"
)

// worktreeStatusEvery is how often the changes of the worktrees beside the
// window's are read again while the section shows: their files are not
// watched, so only a read of them tells what changed. It is statusEvery, as
// the window's own work tree; folded and unread, the section reads them
// only as the worktrees themselves change.
const worktreeStatusEvery = statusEvery

// scmWorktrees is the section of the source control view that lists every
// worktree of the repository with its changes, under the history and the
// repository panel.
type scmWorktrees struct {
	repo    *git.Repo
	key     string
	request *int
	gen     int
	loading bool
	loaded  bool
	err     string
	// remote is set when the repository is read over a connection: its
	// worktrees are listed, but their changes are not read here.
	remote bool
	trees  []*scmWorktree
	closed map[string]bool
	// read is when the changes of the worktrees were read last, which they
	// are again every worktreeStatusEvery while the section shows; force
	// has the next read list the worktrees rather than take those the
	// status read already has.
	read  time.Time
	force bool
}

// scmWorktree is a worktree the section lists: its checkout, and what was
// read of its changes. The window's own worktree is read with the status
// the view already has, so it is never read again here.
type scmWorktree struct {
	wt git.Worktree
	// local is set when the worktree's files can be read here: it is the
	// window's own, or one beside it on this machine.
	local  bool
	status *git.Status
	stat   map[string]git.FileStat
	// groups are the files of status in each group of the view, fileCount
	// how many of them changed once each, and lines the lines they gained
	// and lost: worked out once per read, as a row of it asks for them
	// every frame.
	groups    [groupCount][]git.Entry
	fileCount int
	lines     git.FileStat
	loaded    bool
	reading   bool
	err       string
}

// known reports whether what changed in the worktree was read: its counts
// and its files show then, rather than that it is still read.
func (t *scmWorktree) known() bool { return t.status != nil }

// scmWorktreeRow is a row of the section: its header, a worktree, a file
// of one, or a note.
type scmWorktreeRow struct {
	kind, label, detail string
	tree                *scmWorktree
	e                   git.Entry
	group               scmGroup
	count               int
	stat                git.FileStat
	known               bool
}

// worktreeRead is what the background read of a worktree's changes found.
type worktreeRead struct {
	repo      *git.Repo
	st        *git.Status
	stat      map[string]git.FileStat
	groups    [groupCount][]git.Entry
	fileCount int
	lines     git.FileStat
	err       error
}

// readWorktree opens a worktree and reads its status, its lines, and the
// counts of its files, within the reads the work trees here run at once
// together (#149).
func readWorktree(wt git.Worktree) worktreeRead {
	var out worktreeRead
	readGit(func() {
		r, err := git.Open(wt.Path)
		if err != nil {
			out.err = err
			return
		}
		out.repo = r
		st, err := r.Status()
		if err != nil {
			out.err = err
			return
		}
		out.st = st
		if len(st.Entries) > 0 {
			// A clean work tree has no lines to count.
			out.stat, _ = r.WorkNumstat()
		}
		out.groups = groupStatus(st)
		out.fileCount = worktreeFileCount(out.groups)
		out.lines = treeLines(out.groups, out.stat)
	})
	return out
}

// worktreesKey names what the section read: the repository, its refs, and
// the worktrees its status last listed.
func (a *gitContext) worktreesKey() string {
	g := a.git
	if remote.Is(g.repo.Root) {
		return fmt.Sprintf("%p:remote:%d", g.repo, g.refsGen)
	}
	return fmt.Sprintf("%p:%d:%d", g.repo, g.refsGen, g.worktreesOf.UnixNano())
}

// invalidateScmWorktrees reads the worktrees' changes again when the
// section shows, keeping those it shows until the read lands.
func (a *gitContext) invalidateScmWorktrees() {
	w := &a.scm.worktrees
	w.key, w.request = "", nil
	w.gen++
	a.app.invalidate()
}

// relistScmWorktrees reads the worktrees' changes again, and lists them
// again rather than taking those the status already has, as Refresh does.
func (a *gitContext) relistScmWorktrees() {
	(&a.scm.worktrees).force = true
	a.invalidateScmWorktrees()
}

// refreshScmWorktrees lists the worktrees of the repository and reads the
// changes of those beside the window in the background, once, also while
// the section is folded: its title counts what changed, and a worktree made
// or removed shows there without opening it.
func (a *gitContext) refreshScmWorktrees() {
	g, w := a.git, &a.scm.worktrees
	if g.repo == nil || !a.app.online() || g.status == nil {
		return
	}
	key := a.worktreesKey()
	if w.repo == g.repo && w.key == key {
		return
	}
	if w.repo != g.repo {
		*w = scmWorktrees{}
	}
	w.repo, w.key, w.request = g.repo, key, new(int)
	w.loading, w.loaded, w.err = true, false, ""
	w.gen++
	w.read = a.app.gitNow()
	if w.closed == nil {
		w.closed = map[string]bool{}
	}
	repo, request, offHost, force := g.repo, w.request, remote.Is(g.repo.Root), w.force
	w.force = false
	// The worktrees the status read listed are taken as they are, rather
	// than read again: a folder read over a connection lists none.
	fromStatus := !force && !offHost && g.worktreesAny
	var listed []git.Worktree
	if fromStatus {
		// A copy: the status reads them again on the frames, off this read.
		listed = slices.Clone(g.worktrees)
	}
	bg.Go(func() {
		defer recoverCrash()
		var err error
		if !fromStatus {
			readGit(func() { listed, err = repo.Worktrees() })
		}
		a.app.post(func() {
			if w.repo != repo || w.request != request {
				return
			}
			w.loading = false
			if err != nil {
				w.err = err.Error()
				w.gen++
				return
			}
			a.listScmWorktrees(w, listed, offHost)
			w.remote, w.loaded = offHost, true
			w.gen++
			for _, t := range w.trees {
				if !t.wt.Current {
					a.startScmWorktreeRead(t)
				}
			}
		})
	})
}

// listScmWorktrees lists the worktrees a read returned, keeping what was
// read of those still listed so that their rows keep their counts until
// the new read lands, and dropping what was kept of those gone.
func (a *gitContext) listScmWorktrees(w *scmWorktrees, listed []git.Worktree, offHost bool) {
	prev := w.trees
	trees := make([]*scmWorktree, 0, len(listed))
	for _, t := range listed {
		if t.Bare {
			continue
		}
		next := &scmWorktree{wt: t, local: !offHost && !t.Prunable}
		if old := worktreeAt(prev, t.Path); old != nil && old.wt.Prunable == t.Prunable && old.local == next.local {
			// A worktree read before keeps its counts while it is read
			// again, rather than falling back to "…" every refresh.
			next.status, next.stat = old.status, old.stat
			next.groups, next.fileCount, next.lines = old.groups, old.fileCount, old.lines
		}
		trees = append(trees, next)
	}
	// The window's own worktree leads, as the one to tell apart.
	for i, t := range trees {
		if t.wt.Current && i > 0 {
			copy(trees[1:i+1], trees[0:i])
			trees[0] = t
			break
		}
	}
	w.trees = trees
	// A worktree's files stay folded until asked for, so that the section
	// lists the worktrees and their counts first; one removed leaves
	// nothing folded behind it.
	folded := make(map[string]bool, len(trees))
	for _, t := range trees {
		key := "worktree:" + t.wt.Path
		folded[key] = true
		if _, ok := w.closed[key]; !ok {
			w.closed[key] = true
		}
	}
	for key := range w.closed {
		if !folded[key] {
			delete(w.closed, key)
		}
	}
	if offHost {
		return
	}
	// The diff of a worktree removed has no status left to read again.
	for root := range a.app.outsideGit {
		if worktreeAt(w.trees, root) == nil {
			delete(a.app.outsideGit, root)
		}
	}
	for root := range a.app.outsideRead {
		if worktreeAt(w.trees, root) == nil {
			delete(a.app.outsideRead, root)
		}
	}
}

// autoRefreshScmWorktrees reads the changes of the worktrees beside the
// window's again on its own, every worktreeStatusEvery, while the section
// shows or while a diff of one shows: their files are not watched, so
// nothing else tells that they changed. Folded and unread, it waits for
// the worktrees themselves to change, so that a window left alone does
// not wake up to scan them every few seconds.
func (a *gitContext) autoRefreshScmWorktrees() {
	w := &a.scm.worktrees
	if w.repo != a.git.repo || w.loading || len(w.trees) == 0 {
		return
	}
	if !a.app.settings.ScmWorktreesOpen && !a.app.outsideDiffOpen() {
		return
	}
	now := a.app.gitNow()
	if now.Sub(w.read) < worktreeStatusEvery {
		return
	}
	w.read = now
	for _, t := range w.trees {
		if t.wt.Current || !t.local || t.wt.Prunable || t.reading {
			continue
		}
		// What was read stays until the new read lands.
		t.loaded = false
		a.startScmWorktreeRead(t)
	}
}

// startScmWorktreeRead reads the changes of one worktree in the
// background, as the section lists it or as Retry asks again.
func (a *gitContext) startScmWorktreeRead(t *scmWorktree) {
	w := &a.scm.worktrees
	if !t.local || t.loaded || t.reading || t.wt.Prunable {
		return
	}
	t.reading, t.err = true, ""
	wt := t.wt
	repo, request := w.repo, w.request
	bg.Go(func() {
		defer recoverCrash()
		r := readWorktree(wt)
		a.app.post(func() {
			if w.repo != repo || w.request != request {
				return
			}
			tw := w.treeAt(wt.Path)
			if tw == nil {
				return
			}
			tw.reading, tw.loaded, tw.err = false, true, ""
			if r.err != nil {
				tw.err = r.err.Error()
				w.gen++
				return
			}
			tw.status, tw.stat = r.st, r.stat
			tw.groups, tw.fileCount, tw.lines = r.groups, r.fileCount, r.lines
			if r.repo != nil {
				a.app.rememberWorktreeGit(wt.Path, r.repo, r.st, r.stat)
			}
			w.gen++
		})
	})
}

// treeAt is the worktree of root the section lists, nil when it is not
// listed any more.
func (w *scmWorktrees) treeAt(root string) *scmWorktree { return worktreeAt(w.trees, root) }

// worktreeAt is the worktree of root among trees, nil when it is not one
// of them.
func worktreeAt(trees []*scmWorktree, root string) *scmWorktree {
	for _, t := range trees {
		if sameWorktreePath(t.wt.Path, root) {
			return t
		}
	}
	return nil
}

// worktreeFileCount counts the files that changed in a worktree, once each
// even when staged and not. It is worked out once per read.
func worktreeFileCount(groups [groupCount][]git.Entry) int {
	seen := make(map[string]bool, 8)
	for _, es := range groups {
		for _, e := range es {
			seen[e.Path] = true
		}
	}
	return len(seen)
}

// worktreeChangedFiles counts the files that changed in every other worktree
// the section listed, once each: the badge of its header.
func (a *gitContext) worktreeChangedFiles() int {
	n := 0
	for _, t := range a.scm.worktrees.trees {
		if t.wt.Current || !t.known() {
			continue
		}
		n += t.fileCount
	}
	return n
}

// treeLines sums the lines a worktree's changed files gained and lost,
// once each, as its row shows beside its count. It is worked out once per
// read.
func treeLines(groups [groupCount][]git.Entry, stat map[string]git.FileStat) git.FileStat {
	var total git.FileStat
	seen := make(map[string]bool, 8)
	for _, es := range groups {
		for _, e := range es {
			if seen[e.Path] {
				continue
			}
			seen[e.Path] = true
			s := stat[e.Path]
			total.Added += s.Added
			total.Deleted += s.Deleted
		}
	}
	return total
}

// worktreeLines sums the lines the changed files of every other worktree
// gained and lost, once each, as the header of the section shows beside its
// count.
func (a *gitContext) worktreeLines() git.FileStat {
	var total git.FileStat
	for _, t := range a.scm.worktrees.trees {
		if t.wt.Current || !t.known() {
			continue
		}
		total.Added += t.lines.Added
		total.Deleted += t.lines.Deleted
	}
	return total
}

// scmWorktreeRows appends the rows of the section after those of the
// repository panel: its header, and while it is open, the other worktrees
// and their changes. The window's own worktree is the changes above the
// panel, so the section leaves it out rather than counting it twice.
func (a *gitContext) scmWorktreeRows(rows []scmRow) []scmRow {
	w := &a.scm.worktrees
	if a.git.repo == nil {
		return rows
	}
	others := 0
	for _, t := range w.trees {
		if !t.wt.Current {
			others++
		}
	}
	if others == 0 {
		return rows
	}
	head := len(rows)
	rows = append(rows, scmRow{
		key: "worktrees:header", header: true,
		wt: &scmWorktreeRow{kind: "header", label: locales.ScmWorktrees(), stat: a.worktreeLines(), known: true},
	})
	rows[head].count = a.worktreeChangedFiles()
	if !a.app.settings.ScmWorktreesOpen {
		return rows
	}
	if w.err != "" {
		rows = append(rows, worktreeNote("worktrees:retry", "retry", locales.ScmWorktreesFailed(), w.err, 0, nil))
	}
	if w.loading && len(w.trees) == 0 {
		rows = append(rows, worktreeNote("worktrees:loading", "status", locales.WorktreeReading(), "", 0, nil))
		return rows
	}
	if !w.loaded && len(w.trees) == 0 {
		return rows
	}
	if w.remote {
		rows = append(rows, worktreeNote("worktrees:remote", "status", locales.ScmWorktreesRemote(), "", 0, nil))
	}
	changed, pending, clean, failed, missing := 0, 0, 0, false, false
	for _, t := range w.trees {
		if t.wt.Current {
			continue
		}
		key := "worktree:" + t.wt.Path
		known := t.known()
		n, lines := 0, git.FileStat{}
		if known {
			n, lines = t.fileCount, t.lines
		}
		rows = append(rows, scmRow{key: key, wt: &scmWorktreeRow{
			kind: "tree", tree: t, label: filepath.Base(t.wt.Path), detail: worktreeRef(t.wt), count: n,
			stat: lines, known: known,
		}})
		switch {
		case t.wt.Prunable:
			// A worktree whose folder is gone was not checked: it says
			// nothing of the others being clean.
			missing = true
		case n > 0:
			changed++
		case known:
			clean++
		}
		if !known && t.local && t.err == "" {
			pending++
		}
		if t.err != "" {
			failed = true
		}
		if w.closed[key] {
			continue
		}
		switch {
		case t.wt.Prunable:
			continue
		case t.err != "":
			rows = append(rows, worktreeNote(key+":error", "retryTree", locales.ScmWorktreeFailed(), t.err, 1, t))
			continue
		case !t.local:
			continue
		case !known:
			rows = append(rows, worktreeNote(key+":loading", "status", locales.WorktreeReading(), "", 1, t))
			continue
		}
		for _, g := range []scmGroup{groupStaged, groupMerge, groupChanges, groupUntracked} {
			for _, e := range t.groups[g] {
				rows = append(rows, scmRow{
					key: key + ":" + fmt.Sprint(int(g)) + ":" + e.Path, depth: 1,
					wt: &scmWorktreeRow{kind: "file", tree: t, e: e, group: g, label: filepath.Base(e.Path),
						stat: t.stat[e.Path], known: true},
				})
			}
		}
	}
	// Every worktree that was checked is clean: one whose folder is gone
	// was not, so it does not say so.
	if changed == 0 && pending == 0 && clean > 0 && !failed && !missing && !w.remote {
		rows = append(rows, worktreeNote("worktrees:clean", "status", locales.ScmWorktreesClean(), "", 0, nil))
	}
	return rows
}

// worktreeNote is a row of the section that says something of it, rather
// than a worktree or one of its files.
func worktreeNote(key, kind, label, detail string, depth int, t *scmWorktree) scmRow {
	return scmRow{key: key, depth: depth, wt: &scmWorktreeRow{kind: kind, label: label, detail: detail, tree: t}}
}

// worktreeRef is what a worktree row shows of its branch: its name, or the
// commit it is at when detached.
func worktreeRef(wt git.Worktree) string {
	if wt.Branch != "" {
		return wt.Branch
	}
	if wt.Head != "" {
		return git.ShortHash(wt.Head)
	}
	return ""
}

// worktreeFileDir is the folder of a file of a worktree, relative to it, as
// the rows of a group show it beside the name: "" for a file at its root,
// or one not under it.
func worktreeFileDir(t *scmWorktree, path string) string {
	if t == nil {
		return ""
	}
	rel, err := filepath.Rel(filepath.Clean(t.wt.Path), filepath.Clean(path))
	if err != nil {
		return ""
	}
	// A slash path, as a folder shows on every platform.
	if dir := filepath.ToSlash(filepath.Dir(rel)); dir != "." && dir != ".." && !strings.HasPrefix(dir, "../") {
		return dir
	}
	return ""
}

// sameWorktreePath reports whether two spellings name the same worktree: the
// same folder spelled through a link is one worktree, as /var and /private/var
// name the same folders on macOS, where a worktree made under one is listed
// under the other.
func sameWorktreePath(a, b string) bool {
	if a == b {
		return true
	}
	ra, rb := worktreeRealPath(a), worktreeRealPath(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(ra, rb)
	}
	return ra == rb
}

// worktreeRealPath is the folder a worktree root names, with the links on the
// way resolved, so that two spellings of it compare equal. A root over a
// connection names a folder on the host, which is not resolved here, and one
// that cannot be resolved, as a worktree whose folder is gone, is cleaned as
// it is.
func worktreeRealPath(root string) string {
	if root == "" {
		return ""
	}
	if remote.Is(root) {
		return filepath.Clean(root)
	}
	clean := filepath.Clean(root)
	if real, err := filepath.EvalSymlinks(clean); err == nil {
		return real
	}
	return clean
}

// worktreeMapKey names a worktree in the maps the window keeps of those it
// does not list: its root with the links on the way resolved, as
// sameWorktreePath reads it, folded on Windows, where the same folder is
// spelled in either case. A worktree keeps one key however its folder is
// spelled, so a diff of it finds what was read of it.
func worktreeMapKey(root string) string {
	key := worktreeRealPath(root)
	if runtime.GOOS == "windows" {
		return strings.ToLower(key)
	}
	return key
}

// worktreeGit is how a diff of a worktree the window does not list reads
// it: the context the section kept of it, or one opened here for a tab
// restored, nil while none is known. A blank context stands for a read
// under way, so that the tab shows nothing rather than that it is no work
// tree.
func (a *app) worktreeGit(root string) *gitContext {
	if root == "" {
		return nil
	}
	key := worktreeMapKey(root)
	if ctx, ok := a.outsideGit[key]; ok {
		return ctx
	}
	if a.outsideRead[key] {
		return &gitContext{app: a, git: &gitState{}}
	}
	if !a.knownWorktree(root) {
		return nil
	}
	if a.outsideRead == nil {
		a.outsideRead = make(map[string]bool)
	}
	a.outsideRead[key] = true
	bg.Go(func() {
		defer recoverCrash()
		r, err := git.Open(root)
		var st *git.Status
		var stat map[string]git.FileStat
		if err == nil {
			if st, err = r.Status(); err == nil {
				stat, _ = r.WorkNumstat()
			}
		}
		a.post(func() {
			delete(a.outsideRead, key)
			if err != nil || r == nil {
				// A tab of it stays blank, rather than opening it every
				// frame; the section's next read puts the state back.
				if _, ok := a.outsideGit[key]; !ok {
					if a.outsideGit == nil {
						a.outsideGit = make(map[string]*gitContext)
					}
					a.outsideGit[key] = &gitContext{app: a, git: &gitState{looked: true}, scm: &scmState{}}
				}
				a.invalidate()
				return
			}
			a.rememberWorktreeGit(root, r, st, stat)
		})
	})
	return &gitContext{app: a, git: &gitState{}}
}

// rememberWorktreeGit keeps what was read of a worktree beside the
// window, for a diff of it to read its files.
func (a *app) rememberWorktreeGit(root string, r *git.Repo, st *git.Status, stat map[string]git.FileStat) {
	if a.outsideGit == nil {
		a.outsideGit = make(map[string]*gitContext)
	}
	a.outsideGit[worktreeMapKey(root)] = &gitContext{
		app: a, identity: r,
		git: &gitState{repo: r, looked: true, status: st, workStat: stat},
		scm: &scmState{},
	}
	a.invalidate()
}

// outsideDiffOpen reports whether a tab shows the changes of a worktree
// beside the window's own, which are read again as they change.
func (a *app) outsideDiffOpen() bool {
	for _, col := range a.cols {
		for _, t := range col.tabs {
			if t.diff == nil || t.diff.repoRoot == "" {
				continue
			}
			if a.git.repo != nil && sameWorktreePath(t.diff.repoRoot, a.git.repo.Root) {
				continue
			}
			if a.knownWorktree(t.diff.repoRoot) {
				return true
			}
		}
	}
	return false
}

// diffWorktreeName names the worktree a tab of the changes belongs to,
// when it is one beside the window's work tree: the tab's title says it,
// to tell the tabs of its worktrees apart. It is "" for the window's own
// work tree, and for a repository the window lists.
func (a *app) diffWorktreeName(root string) string {
	if root == "" || a.git.repo == nil || sameWorktreePath(root, a.git.repo.Root) {
		return ""
	}
	if !a.knownWorktree(root) {
		return ""
	}
	return filepath.Base(filepath.Clean(root))
}

// outsideContext reports whether ctx is one of the worktrees the section
// read, rather than one the window lists.
func (a *app) outsideContext(ctx *gitContext) bool {
	for _, c := range a.repositories.contexts {
		if c == ctx {
			return false
		}
	}
	return ctx.git.repo != nil
}

// knownWorktree reports whether root is a worktree of the folder, as the
// status or the section listed it.
func (a *app) knownWorktree(root string) bool {
	match := func(path string) bool { return sameWorktreePath(path, root) }
	for _, ctx := range a.repositories.contexts {
		for _, t := range ctx.git.worktrees {
			if !t.Bare && match(t.Path) {
				return true
			}
		}
		for _, t := range ctx.scm.worktrees.trees {
			if match(t.wt.Path) {
				return true
			}
		}
	}
	return false
}

// activateScmWorktreeRow opens or closes the section, a worktree, or shows
// the changes of a file of one.
func (a *gitContext) activateScmWorktreeRow(r scmRow) {
	w, q := &a.scm.worktrees, r.wt
	switch q.kind {
	case "header":
		a.app.settings.ScmWorktreesOpen = !a.app.settings.ScmWorktreesOpen
		a.app.settings.Changed()
		if a.app.settings.ScmWorktreesOpen {
			a.invalidateScmWorktrees()
		}
		w.gen++
	case "tree":
		w.closed[r.key] = !w.closed[r.key]
		w.gen++
	case "file":
		a.openWorktreeChange(q, true)
	case "retry":
		a.relistScmWorktrees()
	case "retryTree":
		if q.tree != nil {
			q.tree.loaded, q.tree.err, q.tree.reading = false, "", false
			w.gen++
			a.startScmWorktreeRead(q.tree)
		}
	}
}

// openWorktreeChange shows the changes of a file of another worktree, in
// the tab of that worktree rather than the window's.
func (a *gitContext) openWorktreeChange(q *scmWorktreeRow, focus bool) {
	if q.tree == nil {
		return
	}
	base := diffIndex
	if q.group == groupStaged {
		base = diffStaged
	}
	a.app.showChanges(diffTarget{path: q.e.Path, base: base}, focus, q.tree.wt.Path)
}

// scmWorktreeRowView is a row of the section: its header, a worktree, a
// file of one, or a note.
func (a *gitContext) scmWorktreeRowView(c *ui.Context, r scmRow, action *func()) {
	q := r.wt
	if q.kind == "header" {
		a.scmWorktreesHeader(c, r, action)
		return
	}
	p := a.app.pal
	row := ui.Row(c).Grow(1).Gap(5).AlignItems(ui.Center).Height(22).
		Padding(0, 4, 0, scmPanelIndent(r.depth))
	hovered, clicked := row.Hovered(), row.Clicked()
	buttonHovered := false
	if hovered && (q.kind == "tree" || q.kind == "file") {
		row.Background(p.hover)
	}
	row.Transition(hoverFade)
	row.ContextMenu(func(m *ui.Menu) { a.scmWorktreeMenu(m, r, action) })
	tip := q.label
	if q.detail != "" {
		tip += "\n" + q.detail
	}
	if q.tree != nil && q.tree.wt.Path != "" {
		tip += "\n" + shortenHome(q.tree.wt.Path)
	}
	if q.kind == "file" {
		// A click on the row shows the changes of the file, rather than
		// opening it: the buttons beside it open it.
		tip = locales.ScmWorktreeShowChanges() + "\n" + shortenHome(q.e.Path)
	}
	row.Tooltip(tip)
	scope := q.label
	if q.tree != nil {
		scope += " " + q.tree.wt.Path
	}
	button := func(icon *ui.SVG, key, label, tip string, run func()) {
		btn := ui.ButtonBase(c.Key(key+" "+scope)).Size(20, 20).Radius(4).Justify(ui.Center).
			AlignItems(ui.Center).Label(label).Tooltip(tip)
		col := p.muted
		if btn.Hovered() {
			buttonHovered = true
			btn.Background(p.hover)
			col = p.text
		}
		btn.Transition(revealFade)
		pressPose(btn)
		btn.Children(func() { ui.Icon(c, icon).FontSize(13).TextColor(col) })
		if btn.Clicked() {
			*action = run
		}
	}
	row.Children(func() {
		switch q.kind {
		case "tree":
			chevron(c, !a.scm.worktrees.closed[r.key]).FontSize(12).TextColor(p.muted)
			ui.Icon(c, iconFolderGit).FontSize(13).TextColor(p.muted)
			col := p.text
			if q.tree.wt.Current {
				col = p.accent
			}
			ui.Text(c, q.label).FontSize(12.5).TextColor(col).SingleLine().Shrink(1)
			if q.detail != "" {
				ui.Text(c, q.detail).FontSize(11).TextColor(p.muted).SingleLine().Shrink(2)
			}
			if q.tree.wt.Current {
				ui.Text(c, locales.ScmWorktreeCurrent()).FontSize(10.5).TextColor(p.accent).Shrink(0)
			}
			switch {
			case q.tree.wt.Prunable:
				ui.Text(c, locales.WorktreeMissingHint()).FontSize(10.5).TextColor(p.faint).Shrink(0)
			case q.tree.wt.Locked:
				ui.Text(c, locales.WorktreeLockedHint()).FontSize(10.5).TextColor(p.faint).Shrink(0)
			}
			ui.Spacer(c)
			if hovered {
				path, branch := q.tree.wt.Path, q.tree.wt.Branch
				button(iconExternal, "open-worktree", locales.ScmWorktreeOpen(), locales.ScmWorktreeOpen(), func() {
					a.askOpenWorktree(branch, path)
				})
				button(iconCopy, "copy-worktree", locales.CopyPath(), locales.CopyPath(), func() {
					a.app.services.WriteClipboard(hostPath(path))
				})
				if q.tree.wt.Prunable {
					button(iconTrash, "prune-worktree", locales.ScmWorktreePrune(), locales.ScmWorktreePrune(), func() {
						a.askPruneWorktree(branch, path)
					})
				}
			}
			switch {
			case q.tree.wt.Prunable:
			case !q.known:
				if q.tree.local {
					ui.Text(c, "…").FontSize(11).TextColor(p.faint).Shrink(0)
				}
			case q.count == 0:
				ui.Text(c, locales.WorktreeClean()).FontSize(10.5).TextColor(p.faint).Shrink(0)
			default:
				// The lines this worktree's files gained and lost together.
				if q.stat.Added > 0 {
					ui.Text(c, fmt.Sprintf("+%d", q.stat.Added)).FontSize(11.5).FontWeight(500).TextColor(p.added).Shrink(0)
				}
				if q.stat.Deleted > 0 {
					ui.Text(c, fmt.Sprintf("−%d", q.stat.Deleted)).FontSize(11.5).FontWeight(500).TextColor(p.removed).
						Shrink(0).Padding(0, 0, 0, 4)
				}
				ui.Text(c, fmt.Sprint(q.count)).FontSize(11.5).TextColor(p.muted).Padding(0, 5).Radius(8).
					Background(p.hover).ContentTransition(ui.ContentTransition{Numeric: true})
			}
		case "file":
			e := q.e
			letter := e.Letter()
			if q.group == groupStaged {
				letter = e.X
			}
			name := filepath.Base(e.Path)
			icon, col := entryIcon(p, e, name)
			ui.Icon(c, icon).FontSize(14).TextColor(col)
			label := ui.Text(c, name).FontSize(13.5).SingleLine().Shrink(0).TextColor(gitColor(p, letter))
			if letter == 'D' {
				label.Strikethrough()
			}
			// The folder of the file, to tell files of the same name apart.
			if dir := worktreeFileDir(q.tree, e.Path); dir != "" {
				ui.Text(c, dir).FontSize(12).TextColor(p.faint).SingleLine().Shrink(1)
			}
			ui.Spacer(c)
			if hovered {
				path := e.Path
				button(iconOpenFile, "open-change", locales.OpenFile(), openLabel(e), func() {
					a.app.openPath(path, true)
				})
				button(iconCopy, "copy-change", locales.CopyPath(), locales.CopyPath(), func() {
					a.app.services.WriteClipboard(hostPath(path))
				})
			}
			if q.known {
				if q.stat.Added > 0 {
					ui.Text(c, fmt.Sprintf("+%d", q.stat.Added)).FontSize(11.5).FontWeight(500).TextColor(p.added).Shrink(0)
				}
				if q.stat.Deleted > 0 {
					ui.Text(c, fmt.Sprintf("−%d", q.stat.Deleted)).FontSize(11.5).FontWeight(500).TextColor(p.removed).Shrink(0)
				}
			}
			if q.group == groupStaged {
				ui.Text(c, locales.ScmWorktreeStaged()).FontSize(10.5).TextColor(p.accent).Shrink(0)
			}
			ui.Text(c, string(letter)).FontSize(11.5).FontWeight(600).Font(a.app.font.family).Padding(0, 4, 0, 2).
				TextColor(gitColor(p, letter)).Shrink(0)
		default:
			col := p.muted
			if q.kind == "retry" || q.kind == "retryTree" {
				col = p.danger
			}
			ui.Text(c, q.label).FontSize(11.5).TextColor(col).SingleLine().Shrink(1).Grow(1)
			if q.kind == "retry" || q.kind == "retryTree" {
				button(iconRefresh, "retry", locales.Refresh(), locales.Refresh(), func() {
					a.activateScmWorktreeRow(r)
				})
			}
		}
	})
	if clicked && *action == nil && !buttonHovered {
		*action = func() { a.activateScmWorktreeRow(r) }
	}
}

// scmWorktreesHeader is the row heading the section: a click opens or
// closes it, and a refresh reads it again while the pointer is on it.
func (a *gitContext) scmWorktreesHeader(c *ui.Context, r scmRow, action *func()) {
	p := a.app.pal
	open := a.app.settings.ScmWorktreesOpen
	row := ui.Row(c).Grow(1).Height(26).Gap(4).Padding(0, 4, 0, 0).AlignItems(ui.Center).Background(p.chrome)
	hovered, clicked := row.Hovered(), row.Clicked()
	row.Children(func() {
		chevron(c, open).FontSize(12).TextColor(p.muted)
		size, spacing := p.titleLook(11, 0.4)
		ui.Text(c, p.title(r.wt.label)).FontSize(size).FontWeight(600).LetterSpacing(spacing).TextColor(p.muted).
			SingleLine().Shrink(1)
		ui.Spacer(c)
		if r.wt.known && (r.wt.stat.Added > 0 || r.wt.stat.Deleted > 0) {
			// The lines every worktree's files gained and lost together.
			if r.wt.stat.Added > 0 {
				ui.Text(c, fmt.Sprintf("+%d", r.wt.stat.Added)).FontSize(11.5).FontWeight(500).TextColor(p.added).Shrink(0)
			}
			if r.wt.stat.Deleted > 0 {
				ui.Text(c, fmt.Sprintf("−%d", r.wt.stat.Deleted)).FontSize(11.5).FontWeight(500).TextColor(p.removed).
					Shrink(0).Padding(0, 0, 0, 4)
			}
		}
		if hovered {
			if a.app.smallButton(c, iconRefresh, locales.Refresh()+" "+r.wt.label) {
				*action = a.relistScmWorktrees
			}
		}
		ui.Text(c, fmt.Sprint(r.count)).FontSize(11.5).TextColor(p.muted).Padding(0, 5).Radius(8).
			Background(p.hover).ContentTransition(ui.ContentTransition{Numeric: true})
	})
	if clicked && *action == nil {
		*action = func() { a.activateScmWorktreeRow(r) }
	}
}

// scmWorktreeMenu is the menu of a row of the section.
func (a *gitContext) scmWorktreeMenu(m *ui.Menu, r scmRow, action *func()) {
	q, repo := r.wt, a.git.repo
	deferred := func() {
		if *action == nil {
			return
		}
		run := *action
		*action = func() {
			if a.git.repo != repo {
				return
			}
			run()
		}
	}
	items := false
	switch q.kind {
	case "tree":
		if q.tree == nil {
			return
		}
		items = true
		wt := q.tree.wt
		if m.Item(locales.ScmWorktreeOpen()).Chosen() {
			*action = func() { a.askOpenWorktree(wt.Branch, wt.Path) }
		}
		if m.Item(locales.CopyPath()).Chosen() {
			*action = func() { a.app.services.WriteClipboard(hostPath(wt.Path)) }
		}
		if wt.Prunable && m.Item(locales.ScmWorktreePrune()).Chosen() {
			*action = func() { a.askPruneWorktree(wt.Branch, wt.Path) }
		}
	case "file":
		items = true
		if m.Item(locales.OpenFile()).Chosen() {
			*action = func() { a.app.openPath(q.e.Path, true) }
		}
		if m.Item(locales.CopyPath()).Chosen() {
			*action = func() { a.app.services.WriteClipboard(hostPath(q.e.Path)) }
		}
	}
	if items {
		m.Separator()
	}
	if m.Item(locales.Refresh()).Chosen() {
		*action = a.relistScmWorktrees
	}
	deferred()
}
