package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"mayoi/internal/cli"
	"mayoi/internal/remote"
	"mayoi/internal/settings"
	"mayoi/locales"
)

// railFixture is a window of project tabs with a project for each folder,
// the first shown, built by a tester as the window builds it.
func railFixture(t testing.TB, dirs ...string) (*frame, []*desktop, *ui.Tester) {
	t.Helper()
	s := settings.Default()
	s.ProjectTabs = true
	m := &mayoi{s: s}
	f := &frame{m: m}
	var hs []*desktop
	for _, dir := range dirs {
		w := s.NewWindow()
		w.Folder = dir
		// The app asks a fake host for what a window does: the tests have
		// none.
		h := &desktop{m: m}
		h.a = newApp(&fakeHost{}, s, w)
		t.Cleanup(h.a.shutdown)
		f.add(h)
		hs = append(hs, h)
	}
	f.active = hs[0]
	f.setTarget()
	m.wins = slices.Clone(hs)
	tt := ui.NewTester(f.view, 1200, 760)
	step(tt)
	return f, hs, tt
}

// The rail has a square for each project, named by its folder.
func TestRailShowsTheProjects(t *testing.T) {
	root, other := workspaceFixture(t), workspaceFixture(t)
	f, hs, tt := railFixture(t, root, other)
	for _, dir := range []string{root, other} {
		if _, ok := tt.Find(filepath.Base(dir)); !ok {
			t.Fatalf("no square for %s on the rail: %v", dir, tt.Texts())
		}
	}
	if !slices.Equal(f.m.s.Rail, []int{hs[0].a.window.ID, hs[1].a.window.ID}) {
		t.Errorf("rail order %v", f.m.s.Rail)
	}
	if err := tt.RightClick(filepath.Base(other)); err != nil {
		t.Fatal(err)
	}
	if menu := tt.Menu(); !slices.Contains(menu, "Close Project") {
		t.Errorf("the square's menu: %v", menu)
	}
	tt.CloseMenu()
}

// A window of one project keeps the rail, with its square and the button
// that opens another folder, while Project Tabs is on, and has none off.
func TestRailWithOneProject(t *testing.T) {
	root := workspaceFixture(t)
	f, _, tt := railFixture(t, root)
	if _, ok := tt.Find(filepath.Base(root)); !ok {
		t.Errorf("no square for %s on the rail: %v", root, tt.Texts())
	}
	if _, ok := tt.Find(locales.CmdFileOpenFolder()); !ok {
		t.Errorf("no button that opens another folder on the rail: %v", tt.Texts())
	}
	f.m.s.ProjectTabs = false
	step(tt)
	if _, ok := tt.Find(locales.CmdFileOpenFolder()); ok {
		t.Error("the rail is still there with Project Tabs off")
	}
}

// A project out of sight keeps its caret and where it was scrolled, and
// gets them back as it shows again, although its text area is a new one.
func TestProjectKeepsItsPlace(t *testing.T) {
	root, other := workspaceFixture(t), workspaceFixture(t)
	path := filepath.Join(root, "long.go")
	writeLines(t, path, 400)
	f, hs, tt := railFixture(t, root, other)
	a := hs[0].a
	a.openFile(path, true)
	h := &harness{t: t, a: a, tt: tt}
	h.settle()
	ed := a.activeEditor()
	if ed == nil {
		t.Fatal("no editor")
	}
	at := strings.Index(ed.buf.String(), "line 200")
	ed.pending = &[2]int{at, at}
	step(tt)
	step(tt)
	sel, y := ed.sel, ed.vscroll.Y
	if sel != [2]int{at, at} || y <= 0 {
		t.Fatalf("before: sel %v, scrolled %v", sel, y)
	}
	f.active = hs[1]
	step(tt)
	step(tt)
	if !ed.hidden {
		t.Fatal("the editor out of sight is not parked")
	}
	f.active = hs[0]
	step(tt)
	step(tt)
	if ed.sel != sel || ed.vscroll.Y != y {
		t.Fatalf("back: sel %v (was %v), scrolled %v (was %v)", ed.sel, sel, ed.vscroll.Y, y)
	}
}

// What is posted to a project out of sight runs, as the window draws.
func TestHiddenProjectRunsItsPosts(t *testing.T) {
	root, other := workspaceFixture(t), workspaceFixture(t)
	f, hs, tt := railFixture(t, root, other)
	f.active = hs[1]
	step(tt)
	f.active = hs[0]
	step(tt)
	ran := false
	hs[1].a.post(func() { ran = true })
	step(tt)
	if !ran {
		t.Error("a hidden project's post never ran")
	}
}

// Each project keeps what its sidebar shows and its panel's height, as
// another shows meanwhile; how wide the sidebar is and whether it is
// closed are every project's.
func TestProjectKeepsItsLayout(t *testing.T) {
	root, other := workspaceFixture(t), workspaceFixture(t)
	f, hs, tt := railFixture(t, root, other)
	a, b := hs[0].a, hs[1].a
	a.showSourceControl()
	a.window.SidebarWidth, a.window.PanelHeight = 320, 400
	step(tt)
	if !tt.HasText("SOURCE CONTROL") {
		t.Fatalf("A's sidebar: %v", tt.Texts())
	}
	f.active = hs[1]
	step(tt)
	step(tt)
	if tt.HasText("SOURCE CONTROL") || !tt.HasText("EXPLORER") || b.window.SidebarWidth != 320 || b.window.PanelHeight == 400 {
		t.Fatalf("B's sidebar: %+v, %v", b.window.Layout, tt.Texts())
	}
	b.toggleSidebar()
	step(tt)
	f.active = hs[0]
	step(tt)
	step(tt)
	if tt.HasText("SOURCE CONTROL") || !a.window.SidebarClosed || a.window.SidebarView != "git" || a.window.SidebarWidth != 320 || a.window.PanelHeight != 400 {
		t.Fatalf("A's sidebar again: %+v, %v", a.window.Layout, tt.Texts())
	}
}

// A folder opens again with the layout it had as it closed, but for how
// wide the sidebar is and whether it is closed, which stay the window's.
func TestFolderKeepsItsLayout(t *testing.T) {
	root := workspaceFixture(t)
	s := settings.Default()
	s.AddRecent(root)
	w := s.NewWindow()
	w.Folder, w.Expanded = root, []string{"src"}
	w.SidebarView, w.SidebarWidth = "git", 320
	s.Forget(w)
	x := s.NewWindow()
	a := newApp(&fakeHost{folder: root}, s, x)
	t.Cleanup(a.shutdown)
	width := x.SidebarWidth
	a.openSession(root)
	if x.SidebarView != "git" || x.SidebarWidth != width {
		t.Fatalf("layout %+v", x.Layout)
	}
}

// A project of the rail not shown since the app started keeps the session
// the settings have of it as the app quits.
func TestUnshownProjectKeepsItsSession(t *testing.T) {
	s := settings.Default()
	w := s.NewWindow()
	w.Columns = []settings.Column{{Tabs: []settings.Tab{{File: "/tmp/a.go"}}, Weight: 1}}
	a := newApp(&fakeHost{}, s, w)
	t.Cleanup(a.shutdown)
	a.keepSession()
	a.park()
	if len(w.Columns) != 1 || len(w.Columns[0].Tabs) != 1 {
		t.Fatalf("session lost: %+v", w.Columns)
	}
}

// With project tabs, a folder chosen opens in a tab of its own, and a
// window with no folder takes it.
func TestProjectTabsOpenFoldersInTabs(t *testing.T) {
	root, other := workspaceFixture(t), workspaceFixture(t)
	s := settings.Default()
	s.ProjectTabs = true
	h := &fakeHost{}
	a := newApp(h, s, s.NewWindow())
	t.Cleanup(a.shutdown)
	a.switchFolder(root)
	if a.root != root || len(h.windows) != 0 {
		t.Fatalf("no folder: folder %q, windows %v", a.root, h.windows)
	}
	a.switchFolder(other)
	if a.root != root || !slices.Equal(h.windows, []string{other}) {
		t.Fatalf("with a folder: folder %q, windows %v", a.root, h.windows)
	}
}

// writeLines writes a file of n numbered lines.
func writeLines(t *testing.T, path string, n int) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "// line %d\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The square of a project not shown counts the files its work tree has
// changed, read in the background: what switching to it would show.
func TestRailSquareCountsTheChangesOfAnotherProject(t *testing.T) {
	hs := newHarness(t, true)
	hs.gitRepo()
	f, tabs, tt := railFixture(t, hs.root, workspaceFixture(t))
	writeLines(t, hs.path("main.go"), 40)
	writeLines(t, hs.path("README.md"), 9)
	deadline := time.Now().Add(3 * time.Second)
	for tabs[0].changes != 2 && time.Now().Before(deadline) {
		step(tt)
		tabs[0].a.drain()
		time.Sleep(5 * time.Millisecond)
	}
	if got := tabs[0].changes; got != 2 {
		t.Fatalf("the other project counts %d changes, want 2", got)
	}
	// The square shown reads its own count too, and the folder that is
	// not a repository has none.
	step(tt)
	tabs[1].a.drain()
	if got := tabs[1].changes; got != 0 {
		t.Errorf("a folder that is not a repository counts %d changes", got)
	}
	if f.active != tabs[0] {
		t.Errorf("the window shows %q, not %q", f.active.a.folder(), tabs[0].a.folder())
	}
	if dir := os.Getenv("SHOTS"); dir != "" {
		out, err := os.Create(filepath.Join(dir, "rail-changes.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		png.Encode(out, tt.Image())
	}
}

// The squares of the rail name their projects, and the rail is as wide as
// the longest name, within railW and railMaxW.
func TestRailNamesItsProjects(t *testing.T) {
	root := t.TempDir()
	var dirs []string
	for _, name := range []string{"mayoi", "code-lab"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	f, _, tt := railFixture(t, dirs...)
	for _, dir := range dirs {
		if _, ok := tt.Find(filepath.Base(dir)); !ok {
			t.Fatalf("no square named %q: %v", filepath.Base(dir), tt.Texts())
		}
	}
	// Its rows take two lines, the second of them wider than a short
	// name: the rail is at least the room of the names, and no wider than
	// its most.
	if want := railWidthFor([]string{"mayoi", "code-lab"}, 0); f.rail() < want || f.rail() > railMaxW {
		t.Errorf("the rail is %v wide, want %v to %v", f.rail(), want, float32(railMaxW))
	}
	// A name longer than the rail's most is cut to it, its end in an
	// ellipsis, and the rail holds at railMaxW.
	long := strings.Repeat("a-very-long-repository-name-", 4)
	if got := railWidthFor([]string{long}, 0); got != railMaxW {
		t.Errorf("a long name gives a rail %v wide, want %v", got, float32(railMaxW))
	}
	if got := railFitShort(long, railMaxW-railPad); !strings.HasSuffix(got, "…") {
		t.Errorf("a long name not cut: %q", got)
	}
	if dir := os.Getenv("SHOTS"); dir != "" {
		out, err := os.Create(filepath.Join(dir, "rail-names.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		png.Encode(out, tt.Image())
	}
}

// A project with a logo shows it in the column left of its name, small as
// the square, not as wide as the square is.
func TestRailShowsTheLogoBesideTheName(t *testing.T) {
	root := t.TempDir()
	var dirs []string
	for _, name := range []string{"mayoi", "code-lab"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	f, tabs, tt := railFixture(t, dirs...)
	// A logo far larger than the square, which the square must shrink.
	logo := filepath.Join(root, "logo.png")
	img := image.NewRGBA(image.Rect(0, 0, 96, 96))
	for y := range 96 {
		for x := range 96 {
			img.Set(x, y, color.RGBA{R: 220, G: 60, B: 40, A: 255})
		}
	}
	file, err := os.Create(logo)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	file.Close()
	tabs[0].logoSrc = decodeProjectLogo(logo)
	step(tt)
	if tabs[0].logoSrc == nil {
		t.Fatal("no logo")
	}
	if dir := os.Getenv("SHOTS"); dir != "" {
		out, err := os.Create(filepath.Join(dir, "rail-logo.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		png.Encode(out, tt.Image())
	}
	_ = f
}

// The rail of a window of the projects it usually holds, for the eye: a
// logo, names of a few lengths, and the bar of the project shown.
func TestRailLooksLikeItself(t *testing.T) {
	root := t.TempDir()
	var dirs []string
	for _, name := range []string{"vmk-1.0", "code-lab", "nsgo", ".workbuddy"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	f, tabs, tt := railFixture(t, dirs...)
	logo := filepath.Join(root, "logo.png")
	img := image.NewRGBA(image.Rect(0, 0, 96, 96))
	for y := range 96 {
		for x := range 96 {
			img.Set(x, y, color.RGBA{R: 240, G: 175, B: 30, A: 255})
		}
	}
	file, err := os.Create(logo)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	file.Close()
	tabs[1].logoSrc = decodeProjectLogo(logo)
	step(tt)
	_ = f
	if dir := os.Getenv("SHOTS"); dir != "" {
		out, err := os.Create(filepath.Join(dir, "rail-look.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		png.Encode(out, tt.Image())
	}
}

// A row's second line says the lines its work gained and lost, on the left,
// and when the project last moved, on the right.
func TestRailRowShowsItsWork(t *testing.T) {
	root := t.TempDir()
	var dirs []string
	for _, name := range []string{"vmk-1.0", "code-lab", "nsgo"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	_, tabs, tt := railFixture(t, dirs...)
	now := time.Now()
	tabs[0].added, tabs[0].deleted, tabs[0].changes = 128, 21, 5
	tabs[0].ahead, tabs[0].behind = 2, 1
	tabs[0].activityAt = now
	tabs[1].added, tabs[1].changes = 1200, 1
	tabs[1].activityAt = now.Add(-90 * time.Minute)
	tabs[2].activityAt = now.AddDate(0, 0, -3)
	step(tt)
	if dir := os.Getenv("SHOTS"); dir != "" {
		out, err := os.Create(filepath.Join(dir, "rail-row.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		png.Encode(out, tt.Image())
	}
}

// A compact row keeps the name alone, and centers it: the rail is a
// chooser then, not a dashboard.
func TestRailCompactShowsNamesAlone(t *testing.T) {
	root := t.TempDir()
	var dirs []string
	for _, name := range []string{"vmk-1.0", "code-lab", "nsgo"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	f, tabs, tt := railFixture(t, dirs...)
	now := time.Now()
	for i, h := range tabs {
		h.added, h.deleted, h.changes = 100+i, 20, 3
		h.activityAt = now.Add(-time.Duration(i) * time.Hour)
	}
	if got, _, _ := railRow(""); got != railRowH {
		t.Fatalf("a full row is %v tall, want %v", got, float32(railRowH))
	}
	if got, nameY, subY := railRow("compact"); got != float32(railSquare) || nameY != float32(railSquare)/2 || subY != 0 {
		t.Fatalf("a compact row is %v at %v/%v, want %v at %v", got, nameY, subY, float32(railSquare), float32(railSquare)/2)
	}
	f.m.s.RailRows = "compact"
	step(tt)
	// The lines of the work are not drawn, and the name is where one
	// line's is.
	if _, ok := tt.Find("+100"); ok {
		t.Fatalf("a compact row still names its lines: %v", tt.Texts())
	}
	if dir := os.Getenv("SHOTS"); dir != "" {
		out, err := os.Create(filepath.Join(dir, "rail-compact.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		png.Encode(out, tt.Image())
	}
}

// The rail's switches make the state clicked the one shown, as the
// sidebar's view switches do, rather than turn a switch the other way.
func TestRailSwitchesSetStates(t *testing.T) {
	dirs := []string{workspaceFixture(t), workspaceFixture(t)}
	f, _, _ := railFixture(t, dirs...)
	if f.m.s.RailOrder != "" || f.m.s.RailRows != "" {
		t.Fatalf("the rail starts %q/%q, want what moved last and both lines", f.m.s.RailOrder, f.m.s.RailRows)
	}
	// Clicking the state already shown changes nothing.
	f.setRailOrder("")
	if f.m.s.RailOrder != "" {
		t.Fatalf("the order is %q, want what moved last", f.m.s.RailOrder)
	}
	f.setRailOrder("manual")
	if f.m.s.RailOrder != "manual" {
		t.Fatalf("the order is %q after manual is clicked, want manual", f.m.s.RailOrder)
	}
	f.setRailOrder("")
	if f.m.s.RailOrder != "" {
		t.Fatalf("the order is %q after what moved last is clicked, want it", f.m.s.RailOrder)
	}
	f.setRailRows("compact")
	if f.m.s.RailRows != "compact" {
		t.Fatalf("the rows are %q after compact is clicked, want compact", f.m.s.RailRows)
	}
	f.setRailRows("")
	if f.m.s.RailRows != "" {
		t.Fatalf("the rows are %q after both lines is clicked, want them", f.m.s.RailRows)
	}
}

// A row says what its branch has to do: the commits to push, to pull, and
// the files changed, each count where the row has room for it.
func TestRailCountsWhatItsBranchHasToDo(t *testing.T) {
	hs := newHarness(t, true)
	git := hs.gitRepo()
	bare := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	git("remote", "add", "origin", bare)
	git("push", "-q", "-u", "origin", "main")
	// Two commits pushed, then the branch back two: behind two.
	writeLines(t, hs.path("two.go"), 3)
	git("add", "two.go")
	git("commit", "-q", "-m", "two")
	writeLines(t, hs.path("three.go"), 3)
	git("add", "three.go")
	git("commit", "-q", "-m", "three")
	git("push", "-q", "origin", "main")
	git("reset", "-q", "--hard", "HEAD~2")
	// One more commit, never pushed: ahead one. And a file changed in the
	// work tree.
	writeLines(t, hs.path("four.go"), 3)
	git("add", "four.go")
	git("commit", "-q", "-m", "four")
	writeLines(t, hs.path("main.go"), 40)

	_, tabs, tt := railFixture(t, hs.root, workspaceFixture(t))
	deadline := time.Now().Add(5 * time.Second)
	for tabs[0].ahead != 1 && time.Now().Before(deadline) {
		step(tt)
		tabs[0].a.drain()
		time.Sleep(5 * time.Millisecond)
	}
	if got := [3]int{tabs[0].ahead, tabs[0].behind, tabs[0].changes}; got != [3]int{1, 2, 1} {
		t.Fatalf("the other project's branch is %d to push, %d to pull, %d files changed", got[0], got[1], got[2])
	}
	if cs := railChips(tabs[0], tabs[0].a.pal); len(cs) != 3 || cs[0].text != "\u21911" || cs[1].text != "\u21932" || cs[2].text != "\u25cf1" {
		t.Errorf("the counts of the row: %+v", cs)
	}
	if dir := os.Getenv("SHOTS"); dir != "" {
		out, err := os.Create(filepath.Join(dir, "rail-branch.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		png.Encode(out, tt.Image())
	}
}

// The rail hears of commits made elsewhere by fetching: without one a
// branch looks even with its upstream, however far it has fallen behind.
func TestRailFetchesWhatItsBranchFellBehind(t *testing.T) {
	hs := newHarness(t, true)
	git := hs.gitRepo()
	bare := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	git("remote", "add", "origin", bare)
	git("push", "-q", "-u", "origin", "main")
	// Another clone pushes a commit, which this one hears of only by
	// fetching.
	other := t.TempDir()
	if out, err := exec.Command("git", "clone", "-q", bare, other).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	writeLines(t, filepath.Join(other, "main.go"), 9)
	for _, args := range [][]string{
		{"add", "main.go"},
		{"commit", "-q", "-m", "elsewhere"},
		{"push", "-q", "origin", "main"},
	} {
		cmd := exec.Command("git", append([]string{"-C", other, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// Not fetched yet: as far as this work tree knows, it is even.
	if out := git("status", "--porcelain=v2", "--branch"); !strings.Contains(out, "+0 -0") {
		t.Fatalf("the branch does not look even with its upstream:\n%s", out)
	}

	f, tabs, tt := railFixture(t, hs.root, workspaceFixture(t))
	// The rail asks its remote only when the preference asks for it, and
	// the read it began as the panel drew came before this: that one is
	// waited for, and the next reads with the preference on.
	f.m.s.RailFetch = true
	for tabs[0].changesBusy {
		step(tt)
		time.Sleep(5 * time.Millisecond)
	}
	tabs[0].changesAt = time.Time{}
	deadline := time.Now().Add(5 * time.Second)
	for tabs[0].behind != 1 && time.Now().Before(deadline) {
		step(tt)
		tabs[0].a.drain()
		time.Sleep(5 * time.Millisecond)
	}
	if got := tabs[0].behind; got != 1 {
		t.Fatalf("the branch is %d behind its upstream, want 1", got)
	}
	if tabs[0].fetchedAt.IsZero() {
		t.Errorf("the row did not keep when it fetched")
	}
}

// A fetch that cannot reach its remote is no reason for the row to say
// nothing: it counts the files changed here from what Git already knows.
func TestRailReadsBeforeItFetches(t *testing.T) {
	hs := newHarness(t, true)
	git := hs.gitRepo()
	// An upstream that never answers.
	git("remote", "add", "origin", "http://127.0.0.1:1/none.git")
	git("config", "branch.main.remote", "origin")
	git("config", "branch.main.merge", "refs/heads/main")
	writeLines(t, hs.path("main.go"), 40)

	_, tabs, tt := railFixture(t, hs.root, workspaceFixture(t))
	deadline := time.Now().Add(5 * time.Second)
	for tabs[0].changes != 1 && time.Now().Before(deadline) {
		step(tt)
		tabs[0].a.drain()
		time.Sleep(5 * time.Millisecond)
	}
	if got := tabs[0].changes; got != 1 {
		t.Fatalf("the work tree has %d files changed, want 1", got)
	}
	if tabs[0].changesBusy {
		t.Errorf("the row waited for a fetch to count the files changed")
	}
}

// A project whose tab was never switched to never drew a frame, so its own
// app runs nothing posted to it. The window's app runs it instead: the row
// shows without a switch to its tab.
// A project's row names the lines its work tree gained and lost, under the
// name, without its tab ever being shown.
func TestRailShowsTheLinesItsWorkAddedAndDeleted(t *testing.T) {
	hs := newHarness(t, true)
	hs.gitRepo()
	dir := filepath.Join(t.TempDir(), "lab")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	writeLines(t, filepath.Join(dir, "README.md"), 3)
	git("add", "README.md")
	git("commit", "-q", "-m", "first")
	// Six lines added, three gone.
	writeLines(t, filepath.Join(dir, "README.md"), 9)

	_, tabs, tt := railFixture(t, hs.root, dir)
	deadline := time.Now().Add(5 * time.Second)
	for tabs[1].added != 6 && time.Now().Before(deadline) {
		step(tt)
		time.Sleep(5 * time.Millisecond)
	}
	if got := tabs[1].added; got != 6 {
		t.Fatalf("the row counts %d lines added, want 6", got)
	}
	if cs := railLines(tabs[1], &palette{}); len(cs) == 0 || cs[0].text != "+6" {
		t.Fatalf("the row lists %v, want +6", cs)
	}
	// Thousands read short, so the name keeps its room.
	if got := railLines(&desktop{added: 3386, deleted: 1234}, &palette{}); len(got) != 2 || got[0].text != "+3.4k" || got[1].text != "\u22121.2k" {
		t.Fatalf("the row lists %v, want +3.4k −1.2k", got)
	}
	// When the project last moved: a change of its work tree is now.
	if since := time.Since(tabs[1].activityAt); since > time.Minute || since < 0 {
		t.Fatalf("the project moved %v ago, want just now", since)
	}
}

// The rail shows the project that moved last first, and one that never
// moved after them all.
func TestRailOrdersItsProjectsByActivity(t *testing.T) {
	now := time.Now()
	old, moved, never := &desktop{}, &desktop{}, &desktop{}
	moved.activityAt = now
	old.activityAt = now.Add(-time.Hour)
	for _, c := range []struct {
		name string
		tabs []*desktop
		want *desktop
	}{
		{"the one that moved last", []*desktop{old, moved}, moved},
		{"one that never moved", []*desktop{never, moved}, moved},
		{"one that never moved last", []*desktop{never, old}, old},
	} {
		if got := railOrder(c.tabs); got[0] != c.want {
			t.Errorf("%s is first, want %v", c.name, c.want)
		}
	}
	// Custom labels from `mayoi projects label` keep their full text on the row.
	s := settings.Default()
	dir := filepath.Join(t.TempDir(), "thing")
	s.SetTabStyle(dir, settings.TabStyle{Label: "Thing: the engine"})
	if got := railTitle(s, dir); got != "Thing: the engine" {
		t.Errorf("railTitle = %q, want full label", got)
	}
}

func TestRailCountsAProjectItNeverShowed(t *testing.T) {
	hs := newHarness(t, true)
	hs.gitRepo()
	dir := filepath.Join(t.TempDir(), "lab")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	writeLines(t, filepath.Join(dir, "README.md"), 3)
	git("add", "README.md")
	git("commit", "-q", "-m", "first")
	writeLines(t, filepath.Join(dir, "README.md"), 9)

	_, tabs, tt := railFixture(t, hs.root, dir)
	deadline := time.Now().Add(5 * time.Second)
	for tabs[1].changes != 1 && time.Now().Before(deadline) {
		// The window draws, and the project's app is left to itself, as it
		// is until its tab is chosen.
		step(tt)
		time.Sleep(5 * time.Millisecond)
	}
	if got := tabs[1].changes; got != 1 {
		t.Fatalf("the project never shown counts %d changes, want 1", got)
	}
}

func TestProjectWords(t *testing.T) {
	for name, want := range map[string]string{
		"mayoi": "M", "my-app": "MA", "mayoi_editor": "ME", "mayoiEditor": "ME", "go.dev": "GD", "": "?",
	} {
		if got := initials(projectWords(name)); got != want {
			t.Errorf("%q: %q, want %q", name, got, want)
		}
	}
}

// The rail tells projects apart by their labels where their initials
// are the same, and labels names the same alike.
func TestRailLabels(t *testing.T) {
	for _, tc := range []struct{ names, want []string }{
		{[]string{"mayoi", "my-app", "go"}, []string{"Ma", "MA", "Go"}},
		{[]string{"Downloads", "Documents", "Desktop"}, []string{"Dw", "Dc", "De"}},
		{[]string{"Downloads", "Downloads", "Desktop"}, []string{"Do", "Do", "De"}},
		{[]string{"Downloads", "Documents", "Downloads"}, []string{"Dw", "Dc", "Dw"}},
		{[]string{"app", "apps"}, []string{"Ap", "As"}},
		{[]string{"my-app", "my-api"}, []string{"Mp", "Mi"}},
		{[]string{"x", "", "下载"}, []string{"X", "?", "下载"}},
	} {
		if got := railLabels(tc.names); !slices.Equal(got, tc.want) {
			t.Errorf("%q: %q, want %q", tc.names, got, tc.want)
		}
	}
}

// A project's own label is up to two characters as a person sees them.
func TestRailOwnLabel(t *testing.T) {
	for s, want := range map[string]string{
		"": "", " DL ": "DL", "abc": "ab", "🚀x": "🚀x", "👍🏽👋🏻z": "👍🏽👋🏻",
		"🇯🇵🇨🇳🇺🇸": "🇯🇵🇨🇳", "👩‍💻!": "👩‍💻!", "é1": "é1",
	} {
		if got := railLabel(s); got != want {
			t.Errorf("%q: %q, want %q", s, got, want)
		}
	}
}

// A project's own label and color are kept by its folder, and forgotten
// once both are its own again.
func TestRailTabStyle(t *testing.T) {
	dir := workspaceFixture(t)
	f, _, _ := railFixture(t, dir, workspaceFixture(t))
	f.setTabStyle(dir, func(st *settings.TabStyle) { st.Label = "DL" })
	f.setTabStyle(dir, func(st *settings.TabStyle) { st.Color = "cyan" })
	if got := f.m.s.ProjectTabStyle[dir]; got != (settings.TabStyle{Label: "DL", Color: "cyan"}) {
		t.Fatalf("style: %+v", got)
	}
	if _, ok := railColor(darkPalette(), "cyan"); !ok {
		t.Error("cyan is not a color")
	}
	if _, ok := railColor(darkPalette(), "plaid"); ok {
		t.Error("plaid is a color")
	}
	f.setTabStyle(dir, func(st *settings.TabStyle) { *st = settings.TabStyle{} })
	if f.m.s.ProjectTabStyle != nil {
		t.Fatalf("styles left: %v", f.m.s.ProjectTabStyle)
	}
}

// tinyWebP is a lossless WebP of one pixel.
const tinyWebP = "UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA=="

// A picture chosen for a project is copied beside the settings under a
// name of its bytes, an SVG as it is, and what does not decode is not.
func TestImportProjectIcon(t *testing.T) {
	src, icons := t.TempDir(), filepath.Join(t.TempDir(), "project-icons")
	writePNG(t, filepath.Join(src, "a.png"), 16, 16)
	data, _ := os.ReadFile(filepath.Join(src, "a.png"))
	os.WriteFile(filepath.Join(src, "b.jpeg.png"), data, 0o644)
	os.WriteFile(filepath.Join(src, "logo.svg"), []byte(testSVG), 0o644)
	webp, _ := base64.StdEncoding.DecodeString(tinyWebP)
	os.WriteFile(filepath.Join(src, "tiny.webp"), webp, 0o644)
	os.WriteFile(filepath.Join(src, "fake.png"), []byte("not a picture"), 0o644)
	os.WriteFile(filepath.Join(src, "notes.txt"), data, 0o644)

	name, err := importProjectIcon(icons, filepath.Join(src, "a.png"))
	if err != nil || !strings.HasSuffix(name, ".png") {
		t.Fatalf("png: %q, %v", name, err)
	}
	if again, _ := importProjectIcon(icons, filepath.Join(src, "b.jpeg.png")); again != name {
		t.Errorf("the same picture kept twice: %q and %q", name, again)
	}
	svg, err := importProjectIcon(icons, filepath.Join(src, "logo.svg"))
	if err != nil || !strings.HasSuffix(svg, ".svg") {
		t.Fatalf("svg: %q, %v", svg, err)
	}
	if got, _ := os.ReadFile(filepath.Join(icons, svg)); string(got) != testSVG {
		t.Errorf("svg copied as %q", got)
	}
	if w, err := importProjectIcon(icons, filepath.Join(src, "tiny.webp")); err != nil || decodeProjectLogo(filepath.Join(icons, w)) == nil {
		t.Errorf("webp: %q, %v", w, err)
	}
	for _, bad := range []string{"fake.png", "notes.txt", "missing.png"} {
		if name, err := importProjectIcon(icons, filepath.Join(src, bad)); err == nil {
			t.Errorf("%s imported as %q", bad, name)
		}
	}
	entries, _ := os.ReadDir(icons)
	if len(entries) != 3 {
		t.Errorf("%d files kept, want 3", len(entries))
	}
	for _, p := range []string{"a.PNG", "x.jpg", "x.jpeg", "x.webp", "x.svg", "x.ico", "x.icns"} {
		if !isIconFile(p) {
			t.Errorf("%s is not an icon file", p)
		}
	}
	for _, p := range []string{"x.gif", "x.txt", "dir"} {
		if isIconFile(p) {
			t.Errorf("%s is an icon file", p)
		}
	}
}

// settleIcon draws frames until the project of dir shows an icon of its
// own, as the copy of it ends in the background.
func settleIcon(t *testing.T, f *frame, tt *ui.Tester, dir string) string {
	t.Helper()
	for range 200 {
		step(tt)
		if icon := f.m.s.ProjectTabStyle[dir].Icon; icon != "" {
			return icon
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no icon set")
	return ""
}

// A picture dropped on a project's square shows in place of its logo and
// label, a label given after takes its place, and the copy is deleted
// once no project shows it.
func TestRailOwnIcon(t *testing.T) {
	dir, other := workspaceFixture(t), workspaceFixture(t)
	f, hs, tt := railFixture(t, dir, other)
	f.m.icons = filepath.Join(t.TempDir(), "project-icons")
	a := hs[0].a
	pic := filepath.Join(t.TempDir(), "pic.png")
	writePNG(t, pic, 16, 16)

	f.setTabStyle(dir, func(st *settings.TabStyle) { st.Label, st.Color = "DL", "cyan" })
	f.setProjectIcon(a, dir, pic)
	icon := settleIcon(t, f, tt, dir)
	if got := f.m.s.ProjectTabStyle[dir]; got != (settings.TabStyle{Color: "cyan", Icon: icon}) {
		t.Fatalf("style after the icon: %+v", got)
	}
	path := f.m.iconPath(icon)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	// The square shows it.
	for range 200 {
		step(tt)
		if hs[0].logoSrc != nil && hs[0].logoIcon == path {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if hs[0].logoIcon != path || hs[0].logoSrc == nil {
		t.Fatalf("the square shows %q, %v", hs[0].logoIcon, hs[0].logoSrc)
	}

	// Another project shows the same picture: the copy is shared.
	f.setProjectIcon(a, other, pic)
	if got := settleIcon(t, f, tt, other); got != icon {
		t.Fatalf("other's icon %q, want %q", got, icon)
	}
	f.askRailLabel(a, dir, filepath.Base(dir))
	if msg := a.prompt.done("🚀"); msg != "" {
		t.Fatal(msg)
	}
	if got := f.m.s.ProjectTabStyle[dir]; got != (settings.TabStyle{Label: "🚀", Color: "cyan"}) {
		t.Fatalf("style after the label: %+v", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the copy other shows is gone: %v", err)
	}

	// Reset Icon on the last project that shows it deletes the copy.
	if err := tt.RightClick(filepath.Base(other)); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Reset Icon"); err != nil {
		t.Fatal(err)
	}
	step(tt)
	if f.m.s.ProjectTabStyle[other].Icon != "" {
		t.Error("other's icon is left")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the copy is left: %v", err)
	}
	// Without an icon, Reset Icon is disabled.
	tt.RightClick(filepath.Base(other))
	if err := tt.ChooseMenuItem("Reset Icon"); err == nil {
		t.Error("Reset Icon without an icon")
	}

	// A picture that does not decode leaves the square as it was.
	bad := filepath.Join(t.TempDir(), "bad.png")
	os.WriteFile(bad, []byte("nope"), 0o644)
	fails := a.fails
	f.setProjectIcon(a, other, bad)
	for range 200 {
		step(tt)
		if a.fails > fails {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if a.fails == fails || f.m.s.ProjectTabStyle[other].Icon != "" {
		t.Errorf("a bad picture: %d fails, style %+v", a.fails-fails, f.m.s.ProjectTabStyle[other])
	}
}

// Without settings, no project takes an icon.
func TestRailOwnIconWithoutSettings(t *testing.T) {
	dir := workspaceFixture(t)
	f, hs, tt := railFixture(t, dir)
	pic := filepath.Join(t.TempDir(), "pic.png")
	writePNG(t, pic, 16, 16)
	f.setProjectIcon(hs[0].a, dir, pic)
	for range 20 {
		step(tt)
		time.Sleep(2 * time.Millisecond)
	}
	if f.m.s.ProjectTabStyle != nil {
		t.Errorf("styles: %v", f.m.s.ProjectTabStyle)
	}
	if f.canSetIcon(dir) || f.canSetIcon("") {
		t.Error("an icon may be set")
	}
}

// The rail of a window of project tabs, in both themes, at twice the
// scale: the project shown with unsaved changes, and another under the
// pointer, two folders named alike, one of them on a host, and a project
// with a label and color of its own.
func TestShotRail(t *testing.T) {
	if os.Getenv("SHOTS") == "" {
		t.Skip()
	}
	for _, dark := range []bool{true, false} {
		var dirs []string
		for _, name := range []string{"mayoi", "my-app", "webServer", "Downloads", "Documents", "Downloads"} {
			dir := filepath.Join(t.TempDir(), name)
			os.MkdirAll(dir, 0o755)
			os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)
			dirs = append(dirs, dir)
		}
		_, hs, tt := railFixture(t, dirs...)
		tt.SetDark(dark)
		tt.SetScale(2)
		a := hs[0].a
		hs[5].a.root = remote.Path("vps", "/home/me/Downloads")
		hs[1].f.setTabStyle(dirs[1], func(st *settings.TabStyle) { *st = settings.TabStyle{Label: "🚀", Color: "cyan"} })
		f := hs[0].f
		f.m.icons = t.TempDir()
		pic := filepath.Join(t.TempDir(), "pic.svg")
		os.WriteFile(pic, []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><circle cx="16" cy="16" r="14" fill="#e5484d"/><path d="M10 17l4 4 8-9" stroke="#fff" stroke-width="3" fill="none"/></svg>`), 0o644)
		f.setProjectIcon(a, dirs[2], pic)
		a.openFile(filepath.Join(dirs[0], "main.go"), true)
		for range 10 {
			step(tt)
		}
		a.focusColumn()
		step(tt)
		tt.Type("// ")
		hs[3].a.alert = "Bell from zsh"
		r, _ := tt.Find("webServer")
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		for range 30 {
			step(tt)
			time.Sleep(10 * time.Millisecond)
		}
		name := "rail-light"
		if dark {
			name = "rail-dark"
		}
		(&harness{t: t, a: a, tt: tt}).shot(name)
	}
}

// A square dragged along the rail goes before the one it is dropped on,
// or at the end, and the rail's order is kept.
func TestRailReorders(t *testing.T) {
	a, b, c := workspaceFixture(t), workspaceFixture(t), workspaceFixture(t)
	f, hs, _ := railFixture(t, a, b, c)
	ids := func() []int {
		var out []int
		for _, h := range f.tabs {
			out = append(out, h.a.window.ID)
		}
		return out
	}
	id := func(i int) int { return hs[i].a.window.ID }
	f.move(hs[2], hs[0])
	if got := ids(); !slices.Equal(got, []int{id(2), id(0), id(1)}) {
		t.Fatalf("after moving the last first: %v", got)
	}
	f.move(hs[2], nil)
	if got := ids(); !slices.Equal(got, []int{id(0), id(1), id(2)}) {
		t.Fatalf("after moving it to the end: %v", got)
	}
	if !slices.Equal(f.m.s.Rail, ids()) {
		t.Errorf("rail kept as %v, shown as %v", f.m.s.Rail, ids())
	}
}

// railDragTo drags the square of from with the pointer to (x, y), as a
// person does, and lets the move it posts run.
func railDragTo(tt *ui.Tester, from string, x, y float32) error {
	r, ok := tt.Find(from)
	if !ok {
		return fmt.Errorf("no square %q", from)
	}
	x0, y0 := r.X+r.W/2, r.Y+r.H/2
	tt.Press(x0, y0)
	step(tt)
	for i := 1; i <= 4; i++ {
		f := float32(i) / 4
		tt.Move(x0+(x-x0)*f, y0+(y-y0)*f)
		step(tt)
	}
	step(tt)
	tt.Release(x, y)
	for range 3 {
		step(tt)
	}
	return nil
}

// Dragged with the pointer, a square goes before the one it is dropped
// on, not to the end, and past the last one to the end.
func TestRailDragsSquares(t *testing.T) {
	// Named so that only the rail shows the names of the projects not
	// shown.
	var dirs []string
	for _, name := range []string{"railalpha", "railbeta", "railgamma"} {
		dir := filepath.Join(t.TempDir(), name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	f, hs, tt := railFixture(t, dirs...)
	f.active = hs[2]
	step(tt)
	order := func() []*desktop { return slices.Clone(f.tabs) }
	r, _ := tt.Find("railalpha")
	if err := railDragTo(tt, "railbeta", r.X+r.W/2, r.Y+r.H/2); err != nil {
		t.Fatal(err)
	}
	if got := order(); !slices.Equal(got, []*desktop{hs[1], hs[0], hs[2]}) {
		t.Fatalf("dropped on the first: %v, want the second before it", got)
	}
	// Below the squares, on the rail itself.
	if err := railDragTo(tt, "railbeta", r.X+r.W/2, 600); err != nil {
		t.Fatal(err)
	}
	if got := order(); !slices.Equal(got, []*desktop{hs[0], hs[2], hs[1]}) {
		t.Fatalf("dropped past the last: %v, want it at the end", got)
	}
}

// A picture dragged from the sidebar onto a square shows in it, and
// leaves the rail's order as it was.
func TestRailTakesPictureFromSidebar(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "railalpha")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	shown := workspaceFixture(t)
	writePNG(t, filepath.Join(shown, "railpic.png"), 16, 16)
	f, hs, tt := railFixture(t, shown, dir)
	f.m.icons = filepath.Join(t.TempDir(), "project-icons")
	for range 200 {
		step(tt)
		if _, ok := tt.Find("railpic.png"); ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	r, _ := tt.Find("railalpha")
	if err := railDragTo(tt, "railpic.png", r.X+r.W/2, r.Y+r.H/2); err != nil {
		t.Fatal(err)
	}
	settleIcon(t, f, tt, dir)
	if !slices.Equal(f.tabs, hs) {
		t.Errorf("the rail is reordered: %v", f.tabs)
	}
}

// The square's menu reveals the folder, copies its path and closes the
// project or the others.
func TestRailMenu(t *testing.T) {
	root, other := workspaceFixture(t), workspaceFixture(t)
	_, _, tt := railFixture(t, root, other)
	if err := tt.RightClick(filepath.Base(other)); err != nil {
		t.Fatal(err)
	}
	menu := tt.Menu()
	for _, want := range []string{revealLabel(), "Copy Path", "Close Project", "Close Other Projects"} {
		if !slices.Contains(menu, want) {
			t.Errorf("no %q in the square's menu: %v", want, menu)
		}
	}
	if err := tt.ChooseMenuItem("Copy Path"); err != nil {
		t.Fatal(err)
	}
	step(tt)
	if got := tt.Clipboard(); got != other {
		t.Errorf("copied %q, want %q", got, other)
	}
}

// BenchmarkRailFrame is a frame of a window of twelve projects, drawn
// with nothing changing, as the rail is built every frame.
func BenchmarkRailFrame(b *testing.B) {
	var dirs []string
	for range 12 {
		dirs = append(dirs, workspaceFixture(b))
	}
	_, _, tt := railFixture(b, dirs...)
	for range 30 {
		step(tt)
	}
	b.ResetTimer()
	for range b.N {
		step(tt)
	}
}

// BenchmarkProjectSwitch is the frame that shows another project, each
// with a long Go file open, as a click on the rail does.
func BenchmarkProjectSwitch(b *testing.B) {
	src, _ := os.ReadFile("editor.go")
	var dirs []string
	for range 2 {
		dir := workspaceFixture(b)
		os.WriteFile(filepath.Join(dir, "editor.go"), src, 0o644)
		dirs = append(dirs, dir)
	}
	f, hs, tt := railFixture(b, dirs...)
	for i, h := range hs {
		f.active = h
		h.a.openFile(filepath.Join(dirs[i], "editor.go"), true)
		for range 30 {
			step(tt)
			time.Sleep(5 * time.Millisecond)
		}
	}
	b.ResetTimer()
	for i := range b.N {
		f.active = hs[i%2]
		step(tt)
	}
}

// A terminal's bell in a project out of sight marks its square, in its
// tip too, until the project shows.
func TestRailMarksAttention(t *testing.T) {
	root, other := workspaceFixture(t), workspaceFixture(t)
	f, hs, tt := railFixture(t, root, other)
	// Shown once, as a project with a terminal was.
	f.active = hs[1]
	step(tt)
	f.active = hs[0]
	step(tt)
	hs[1].a.post(func() { hs[1].a.alert = "Bell from zsh" })
	step(tt)
	if hs[1].a.alert == "" {
		t.Fatal("the mark of a project out of sight went")
	}
	r, _ := tt.Find(filepath.Base(other))
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(time.Second)
	if !tt.HasText("Bell from zsh") {
		t.Errorf("the tip does not say why: %v", tt.Texts())
	}
	f.active = hs[1]
	step(tt)
	if hs[1].a.alert != "" {
		t.Error("the mark stays as the project shows")
	}
}

// What the programs of a project out of sight wait for and work at show
// in the tip of its square, the program that waits first, beside what was
// asked attention for.
func TestRailTipsWork(t *testing.T) {
	root, other := workspaceFixture(t), workspaceFixture(t)
	f, hs, tt := railFixture(t, root, other)
	// Shown once, as a project with a terminal was.
	f.active = hs[1]
	step(tt)
	f.active = hs[0]
	step(tt)
	hs[1].a.post(func() {
		hs[1].a.terms = append(hs[1].a.terms, &session{status: terminal.ProgramStatus{Records: []terminal.StatusRecord{
			{ID: "", State: terminal.StatusWorking, App: "cargo", Message: "Building", Progress: 42},
			{ID: "deploy", State: terminal.StatusBlocked, App: "claude", Message: "Needs approval", Progress: -1},
		}}})
		hs[1].a.alert, hs[1].a.alertState = "codex: Done", terminal.StatusDone
	})
	step(tt)
	r, _ := tt.Find(filepath.Base(other))
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(time.Second)
	for _, want := range []string{"claude: Needs approval", "cargo: Building 42%", "codex: Done"} {
		if !tt.HasText(want) {
			t.Errorf("the tip does not say %q: %v", want, tt.Texts())
		}
	}
}

// findProjectLogo takes the first favicon or logo file, the root before
// public/, and app/ before src/, but no icon alone that does not decode.
func TestFindProjectLogo(t *testing.T) {
	write := func(dir, rel string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	empty := t.TempDir()
	if got := findProjectLogo(empty); got != "" {
		t.Errorf("empty folder: %q", got)
	}
	dir := t.TempDir()
	write(dir, "public/logo.png")
	write(dir, "src/assets/logo.svg")
	if got, want := findProjectLogo(dir), filepath.Join(dir, "public", "logo.png"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	write(dir, "favicon.svg")
	if got, want := findProjectLogo(dir), filepath.Join(dir, "favicon.svg"); got != want {
		t.Errorf("root first: got %q, want %q", got, want)
	}
}

// extractIconHref takes the icon a source declares: link tags in any
// attribute order, or icon metadata, skipping data URIs and remote URLs.
func TestExtractIconHref(t *testing.T) {
	for source, want := range map[string]string{
		`<link rel="icon" href="/brand/mark.svg">`:                             "/brand/mark.svg",
		`<link href="icons/a.png?v=2" rel="shortcut icon">`:                    "icons/a.png",
		`<link REL='APPLE-TOUCH-ICON' HREF='/apple.png'>`:                      "/apple.png",
		`<link rel="stylesheet" href="app.css"><link rel="icon" href="f.svg">`: "f.svg",
		`{ rel: "icon", href: "/icons/root.png" }`:                             "/icons/root.png",
		`{ rel: "icon", url: "/images/icons/favicon-v2.ico" }`:                 "/images/icons/favicon-v2.ico",
		`icons: { icon: "/icon.png", apple: "/apple.png" }`:                    "/icon.png",
		`export const m = { apple: "/ap.png" }`:                                "/ap.png",
		`<link data-href="/wrong.png" href="/r.png" rel="icon">`:               "/r.png",
		`<link rel="icon" href="./site.png">`:                                  "site.png",
		`<link rel="mask-icon" href="m.svg">`:                                  "",
		`<link rel="icon" href="../up.png">`:                                   "",
		`<link rel="icon" href="//host/x.png">`:                                "",
		`<link rel="icon" href="data:image/svg+xml,xxx">`:                      "",
		`<link rel="icon" href="https://example.com/f.png">`:                   "",
		`<link rel="stylesheet" href="app.css">`:                               "",
		`no links here`:                                                        "",
	} {
		if got := extractIconHref(source); got != want {
			t.Errorf("%q: got %q, want %q", source, got, want)
		}
	}
}

// resolveIconHref looks in public/ first for a leading slash, as it names
// the static folder, and beside the source first for a relative href.
func TestResolveIconHref(t *testing.T) {
	for _, c := range []struct {
		href, base string
		want       []string
	}{
		{"/brand/mark.svg", "", []string{"public/brand/mark.svg", "brand/mark.svg"}},
		{"/brand/mark.svg", "site", []string{"public/brand/mark.svg", "brand/mark.svg", "site/brand/mark.svg"}},
		{"icon.png", "", []string{"icon.png", "public/icon.png"}},
		{"icon.png", "site", []string{"site/icon.png", "public/icon.png", "icon.png"}},
		{"./icon.png", "public", []string{"public/icon.png", "icon.png"}},
	} {
		if got := resolveIconHref(c.href, c.base); !slices.Equal(got, c.want) {
			t.Errorf("%q in %q: got %q, want %q", c.href, c.base, got, c.want)
		}
	}
	if resolveIconHref("data:image/png,xxx", "") != nil {
		t.Error("a data URI resolves")
	}
}

// A site of its own in site/ declares its icon beside its page, as
// Tiller's does.
func TestLoadProjectLogoFromSite(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "site", "index.html"), []byte(`<link rel="icon" type="image/png" href="icon.png">`))
	if loadProjectLogo(dir) != nil {
		t.Fatal("a logo without its file")
	}
	writeFile(t, filepath.Join(dir, "site", "icon.png"), pngBytes(t, 16, 16))
	if got := findIconFromSource(dir); got != filepath.Join(dir, "site", "icon.png") {
		t.Errorf("got %q, want site/icon.png", got)
	}
	if loadProjectLogo(dir) == nil {
		t.Error("no logo from the icon site/index.html declares")
	}
}

// A mygo app's icon is the one its configuration names, else
// resources/icon.png, as mygo takes it.
func TestFindMygoIcon(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "resources", "icon.png"), pngBytes(t, 16, 16))
	if findMygoIcon(dir) != "" {
		t.Error("an icon of a folder that is not a mygo app")
	}
	writeFile(t, filepath.Join(dir, "mygo.json"), []byte(`{"name": "App"}`))
	if got, want := findMygoIcon(dir), filepath.Join(dir, "resources", "icon.png"); got != want {
		t.Errorf("default: got %q, want %q", got, want)
	}
	if loadProjectLogo(dir) == nil {
		t.Error("no logo from resources/icon.png")
	}
	writeFile(t, filepath.Join(dir, "art", "app.png"), pngBytes(t, 16, 16))
	writeFile(t, filepath.Join(dir, "mygo.json"), []byte(`{"name": "App", "icon": "art/app.png"}`))
	if got, want := findMygoIcon(dir), filepath.Join(dir, "art", "app.png"); got != want {
		t.Errorf("named: got %q, want %q", got, want)
	}
	writeFile(t, filepath.Join(dir, "mygo.json"), []byte(`{"icon": "../outside.png"}`))
	if got, want := findMygoIcon(dir), filepath.Join(dir, "resources", "icon.png"); got != want {
		t.Errorf("outside the project: got %q, want %q", got, want)
	}
	ts := t.TempDir()
	writeFile(t, filepath.Join(ts, "brand", "icon.png"), pngBytes(t, 16, 16))
	writeFile(t, filepath.Join(ts, "mygo.config.ts"), []byte("export default defineConfig({\n  name: \"App\",\n  icon: \"brand/icon.png\",\n})\n"))
	if got, want := findMygoIcon(ts), filepath.Join(ts, "brand", "icon.png"); got != want {
		t.Errorf("mygo.config.ts: got %q, want %q", got, want)
	}
}

// A macOS app's .icns in its resources decodes, at its PNG entry nearest
// the rail's size.
func TestLoadProjectLogoICNS(t *testing.T) {
	dir := t.TempDir()
	icns := filepath.Join(dir, "app", "Resources", "App.icns")
	writeFile(t, icns, icnsBytes(t, map[string][]byte{
		"ic04": []byte("ARGB not a png"),
		"ic11": pngBytes(t, 32, 32),
		"ic07": pngBytes(t, 128, 128),
		"ic10": pngBytes(t, 1024, 1024),
	}))
	if got := findNativeIcons(dir); !slices.Equal(got, []string{icns}) {
		t.Errorf("got %q, want %q", got, icns)
	}
	bm, ok := loadProjectLogo(dir).(*ui.Bitmap)
	if !ok {
		t.Fatal("no logo from the .icns")
	}
	if w, h := bm.Size(); w != logoSide || h != logoSide {
		t.Errorf("logo is %dx%d, want %dx%d", w, h, logoSide, logoSide)
	}
	img, err := decodeICNS(icnsBytes(t, map[string][]byte{"ic11": pngBytes(t, 32, 32), "ic12": pngBytes(t, 64, 64)}))
	if err != nil {
		t.Fatal(err)
	}
	if w := img.Bounds().Dx(); w != 64 {
		t.Errorf("small entries: took %d, want the largest, 64", w)
	}
	for _, data := range [][]byte{
		[]byte("not an icns"),
		icnsBytes(t, map[string][]byte{"ic04": []byte("ARGB")}),
		append([]byte("icns\x00\x00\x00\x10ic07\xff\xff\xff\xff"), pngBytes(t, 8, 8)...),
	} {
		if _, err := decodeICNS(data); err == nil {
			t.Errorf("%q decoded", data[:min(len(data), 16)])
		}
	}
}

// An Xcode project's AppIcon set is found in a top-level folder's asset
// catalog, its picture nearest the rail's size first.
func TestFindNativeIconsAppIconSet(t *testing.T) {
	dir := t.TempDir()
	set := filepath.Join(dir, "MyApp", "Assets.xcassets", "AppIcon.appiconset")
	writeFile(t, filepath.Join(set, "Contents.json"), []byte(`{"images": [
		{"filename": "16.png", "idiom": "mac", "scale": "1x", "size": "16x16"},
		{"filename": "32@2x.png", "idiom": "mac", "scale": "2x", "size": "32x32"},
		{"filename": "128.png", "idiom": "mac", "scale": "1x", "size": "128x128"},
		{"filename": "512@2x.png", "idiom": "mac", "scale": "2x", "size": "512x512"},
		{"filename": "../escape.png", "idiom": "mac", "scale": "1x", "size": "1024x1024"},
		{"idiom": "mac", "scale": "1x", "size": "256x256"}
	]}`))
	for _, name := range []string{"16.png", "32@2x.png", "128.png", "512@2x.png"} {
		writeFile(t, filepath.Join(set, name), pngBytes(t, 16, 16))
	}
	other := filepath.Join(dir, "MyApp", "Assets.xcassets", "AccentColor.colorset")
	writeFile(t, filepath.Join(other, "Contents.json"), []byte(`{}`))
	// Dependencies are not looked in.
	writeFile(t, filepath.Join(dir, "node_modules", "x", "Assets.xcassets", "AppIcon.appiconset", "Contents.json"), []byte(`{}`))
	want := []string{"128.png", "512@2x.png", "32@2x.png", "16.png"}
	for i, name := range want {
		want[i] = filepath.Join(set, name)
	}
	if got := findNativeIcons(dir); !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if loadProjectLogo(dir) == nil {
		t.Error("no logo from the AppIcon set")
	}
}

// The well-known files come before a mygo app's icon, which comes before
// the icon a source declares, which comes before a native one.
func TestLoadProjectLogoOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Resources", "App.icns"), icnsBytes(t, map[string][]byte{"ic07": pngBytes(t, 128, 128)}))
	writeFile(t, filepath.Join(dir, "site", "index.html"), []byte(`<link rel="icon" href="icon.png">`))
	writeFile(t, filepath.Join(dir, "site", "icon.png"), pngBytes(t, 20, 20))
	writeFile(t, filepath.Join(dir, "mygo.json"), []byte(`{}`))
	writeFile(t, filepath.Join(dir, "resources", "icon.png"), pngBytes(t, 30, 30))
	writeFile(t, filepath.Join(dir, "logo.png"), pngBytes(t, 40, 40))
	side := func() int {
		bm, ok := loadProjectLogo(dir).(*ui.Bitmap)
		if !ok {
			t.Fatal("no logo")
		}
		w, _ := bm.Size()
		return w
	}
	for _, c := range []struct {
		remove string
		want   int
	}{{"", 40}, {"logo.png", 30}, {"mygo.json", 20}, {"site", logoSide}} {
		if c.remove != "" {
			if err := os.RemoveAll(filepath.Join(dir, c.remove)); err != nil {
				t.Fatal(err)
			}
		}
		if got := side(); got != c.want {
			t.Errorf("without %q: a logo %d wide, want %d", c.remove, got, c.want)
		}
	}
}

// writeFile writes data at path, making its folders.
func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// icnsBytes encodes an icns of entries, by type, in the order of their
// types.
func icnsBytes(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var body bytes.Buffer
	for _, typ := range slices.Sorted(maps.Keys(entries)) {
		body.WriteString(typ)
		binary.Write(&body, binary.BigEndian, uint32(8+len(entries[typ])))
		body.Write(entries[typ])
	}
	var b bytes.Buffer
	b.WriteString("icns")
	binary.Write(&b, binary.BigEndian, uint32(8+body.Len()))
	b.Write(body.Bytes())
	return b.Bytes()
}

// An icon no candidate names is found through the sources declaring it.
func TestLoadProjectLogoFromSource(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "index.html")
	if err := os.WriteFile(p, []byte(`<link rel="icon" href="/brand/mark.svg">`), 0o644); err != nil {
		t.Fatal(err)
	}
	if findProjectLogo(dir) != "" {
		t.Fatal("a declared icon is a candidate")
	}
	if loadProjectLogo(dir) != nil {
		t.Fatal("a logo without its file")
	}
	mark := filepath.Join(dir, "public", "brand", "mark.svg")
	if err := os.MkdirAll(filepath.Dir(mark), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mark, []byte(testSVG), 0o644); err != nil {
		t.Fatal(err)
	}
	if loadProjectLogo(dir) == nil {
		t.Error("no logo from the declared icon")
	}
}

// An icon in a non-standard folder is found through Next.js metadata in
// app/layout.tsx.
func TestLoadProjectLogoFromLayout(t *testing.T) {
	dir := t.TempDir()
	layout := filepath.Join(dir, "app", "layout.tsx")
	if err := os.MkdirAll(filepath.Dir(layout), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout, []byte("export const metadata = {\n  icons: {\n    icon: [\n      {\n        rel: \"icon\",\n        type: \"image/x-icon\",\n        url: \"/images/icons/favicon-v2.ico\",\n      },\n    ],\n    apple: \"/images/icons/apple-touch-icon-v2.webp\",\n  },\n};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if loadProjectLogo(dir) != nil {
		t.Fatal("a logo without its file")
	}
	icons := filepath.Join(dir, "public", "images", "icons", "favicon-v2.ico")
	if err := os.MkdirAll(filepath.Dir(icons), 0o755); err != nil {
		t.Fatal(err)
	}
	writeICO(t, icons, pngBytes(t, 16, 16))
	if loadProjectLogo(dir) == nil {
		t.Error("no logo from the metadata-declared icon")
	}
}

// An apple-only declaration resolves through its own key.
func TestLoadProjectLogoFromApple(t *testing.T) {
	dir := t.TempDir()
	layout := filepath.Join(dir, "app", "layout.tsx")
	if err := os.MkdirAll(filepath.Dir(layout), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout, []byte("export const metadata = {\n  icons: {\n    apple: \"/icons/ap.png\",\n  },\n};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	icon := filepath.Join(dir, "public", "icons", "ap.png")
	if err := os.MkdirAll(filepath.Dir(icon), 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, icon, 16, 16)
	if loadProjectLogo(dir) == nil {
		t.Error("no logo from the apple declaration")
	}
}

// The Next.js file convention under src/app/ resolves: icon.ico,
// apple-icon.png.
func TestFindProjectLogoSrcApp(t *testing.T) {
	dir := t.TempDir()
	icon := filepath.Join(dir, "src", "app", "icon.ico")
	if err := os.MkdirAll(filepath.Dir(icon), 0o755); err != nil {
		t.Fatal(err)
	}
	writeICO(t, icon, pngBytes(t, 16, 16))
	if got := findProjectLogo(dir); got != icon {
		t.Errorf("got %q, want %q", got, icon)
	}
	if loadProjectLogo(dir) == nil {
		t.Error("no logo from src/app/icon.ico")
	}
	apple := filepath.Join(dir, "src", "app", "apple-icon.png")
	if err := os.MkdirAll(filepath.Dir(apple), 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, apple, 16, 16)
	if err := os.Remove(icon); err != nil {
		t.Fatal(err)
	}
	if got := findProjectLogo(dir); got != apple {
		t.Errorf("got %q, want %q", got, apple)
	}
}

// loadProjectLogo decodes a project's png or svg logo, and takes nothing
// for a broken file.
func TestLoadProjectLogo(t *testing.T) {
	dir := t.TempDir()
	logo := filepath.Join(dir, "public", "logo.png")
	if err := os.MkdirAll(filepath.Dir(logo), 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, logo, 32, 32)
	if loadProjectLogo(dir) == nil {
		t.Error("no logo from public/logo.png")
	}
	svg := t.TempDir()
	p := filepath.Join(svg, "public", "favicon.svg")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(testSVG), 0o644); err != nil {
		t.Fatal(err)
	}
	if loadProjectLogo(svg) == nil {
		t.Error("no logo from public/favicon.svg")
	}
	broken := t.TempDir()
	bp := filepath.Join(broken, "public", "logo.png")
	if err := os.MkdirAll(filepath.Dir(bp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bp, []byte("not a png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if loadProjectLogo(broken) != nil {
		t.Error("a logo from a broken png")
	}
}

// A large logo is kept no larger than the rail needs, its shape kept.
func TestLoadProjectLogoScalesDown(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "logo.png"), 1024, 512)
	bm, ok := loadProjectLogo(dir).(*ui.Bitmap)
	if !ok {
		t.Fatal("no bitmap from logo.png")
	}
	if w, h := bm.Size(); w != logoSide || h != logoSide/2 {
		t.Errorf("logo is %dx%d, want %dx%d", w, h, logoSide, logoSide/2)
	}
}

// A favicon.ico decodes: its PNG entry, or its 32-bit BMP one.
func TestLoadProjectLogoICO(t *testing.T) {
	dir := t.TempDir()
	ico := filepath.Join(dir, "favicon.ico")
	writeICO(t, ico, pngBytes(t, 16, 16))
	if loadProjectLogo(dir) == nil {
		t.Error("no logo from favicon.ico with a PNG entry")
	}
	bmp := t.TempDir()
	writeICOBMP(t, filepath.Join(bmp, "favicon.ico"))
	if loadProjectLogo(bmp) == nil {
		t.Error("no logo from favicon.ico with a BMP entry")
	}
	if err := os.WriteFile(filepath.Join(bmp, "favicon.ico"), []byte("not an ico"), 0o644); err != nil {
		t.Fatal(err)
	}
	if loadProjectLogo(bmp) != nil {
		t.Error("a logo from a broken ico")
	}
}

// The rail looks for a project's logo on its own, finding none in an
// empty folder, which keeps its initials, and the one a folder has.
func TestProjectIcons(t *testing.T) {
	root, bare := t.TempDir(), t.TempDir()
	logo := filepath.Join(root, "public", "logo.png")
	if err := os.MkdirAll(filepath.Dir(logo), 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, logo, 32, 32)
	_, hs, tt := railFixture(t, root, bare)
	for end := time.Now().Add(settleTimeout); hs[0].logoSrc == nil || !hs[1].logoDone; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("no logo found for a folder with one")
		}
		step(tt)
	}
	if hs[1].logoSrc != nil {
		t.Error("a logo for a folder without one")
	}
}

// A rail with a project's logo builds, as one without does.
func TestRailShowsProjectLogo(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	logo := filepath.Join(root, "public", "logo.png")
	if err := os.MkdirAll(filepath.Dir(logo), 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, logo, 32, 32)
	_, hs, tt := railFixture(t, root, other)
	hs[0].logoDone, hs[0].logoDir = true, root
	hs[0].logoSrc = loadProjectLogo(root)
	step(tt)
	if _, ok := tt.Find(filepath.Base(root)); !ok {
		t.Fatalf("no square for %s on the rail: %v", root, tt.Texts())
	}
}

// decodeDIB keeps the channels and the rows the right way up, top-down or
// bottom-up, and a broken larger entry falls back to a good one.
func TestDecodeICO(t *testing.T) {
	// Top-left red, top-right green, bottom-left blue, bottom-right white.
	want := []color.NRGBA{
		{0xff, 0, 0, 0xff}, {0, 0xff, 0, 0xff},
		{0, 0, 0xff, 0xff}, {0xff, 0xff, 0xff, 0xff},
	}
	dir := t.TempDir()
	bottom := filepath.Join(dir, "bottom.ico")
	writeICOBMP(t, bottom)
	if got := icoPixels(t, bottom); !slices.Equal(got, want) {
		t.Errorf("bottom-up: %v", got)
	}
	for _, doubled := range []bool{false, true} {
		top := filepath.Join(dir, "top.ico")
		writeICOTopDown(t, top, doubled)
		if got := icoPixels(t, top); !slices.Equal(got, want) {
			t.Errorf("top-down doubled=%v: %v", doubled, got)
		}
		os.Remove(top)
	}
	fallback := filepath.Join(dir, "fallback.ico")
	writeICOFallback(t, fallback, pngBytes(t, 16, 16))
	data, err := os.ReadFile(fallback)
	if err != nil {
		t.Fatal(err)
	}
	img, err := decodeICO(data)
	if err != nil {
		t.Fatalf("no fallback: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 16 || b.Dy() != 16 {
		t.Errorf("fallback size %v, want 16×16", b)
	}
}

// A lying DIB header errors instead of panicking or allocating.
func TestDecodeDIBLyingHeader(t *testing.T) {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, uint32(40))
	binary.Write(&b, binary.LittleEndian, int32(0x7fffffff))
	binary.Write(&b, binary.LittleEndian, int32(-2147483648))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint16(32))
	binary.Write(&b, binary.LittleEndian, uint32(0))
	if _, err := decodeDIB(b.Bytes()); err == nil {
		t.Error("no error for a lying header")
	}
	// A PNG entry lying about 8000×8000 is skipped, not decoded.
	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, uint16(0))
	binary.Write(&ico, binary.LittleEndian, uint16(1))
	binary.Write(&ico, binary.LittleEndian, uint16(1))
	ico.Write([]byte{32, 32, 0, 0})
	binary.Write(&ico, binary.LittleEndian, uint16(1))
	binary.Write(&ico, binary.LittleEndian, uint16(32))
	ihdr := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 25)...)
	binary.BigEndian.PutUint32(ihdr[16:20], 8000)
	binary.BigEndian.PutUint32(ihdr[20:24], 8000)
	binary.Write(&ico, binary.LittleEndian, uint32(len(ihdr)))
	binary.Write(&ico, binary.LittleEndian, uint32(6+16))
	ico.Write(ihdr)
	if _, err := decodeICO(ico.Bytes()); err == nil {
		t.Error("a bomb PNG entry decoded")
	}
}

// pngBytes encodes a w×h red png.
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{0xff, 0, 0, 0xff})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// writeICO writes an ico of one entry holding data.
func writeICO(t *testing.T, path string, data []byte) {
	t.Helper()
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, uint16(0))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	b.Write([]byte{32, 32, 0, 0})
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint16(32))
	binary.Write(&b, binary.LittleEndian, uint32(len(data)))
	binary.Write(&b, binary.LittleEndian, uint32(6+16))
	b.Write(data)
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeICOBMP writes an ico of one 2×2 32-bit BMP entry.
func writeICOBMP(t *testing.T, path string) {
	t.Helper()
	var dib bytes.Buffer
	binary.Write(&dib, binary.LittleEndian, uint32(40))
	binary.Write(&dib, binary.LittleEndian, int32(2))
	binary.Write(&dib, binary.LittleEndian, int32(4))
	binary.Write(&dib, binary.LittleEndian, uint16(1))
	binary.Write(&dib, binary.LittleEndian, uint16(32))
	binary.Write(&dib, binary.LittleEndian, uint32(0))
	dib.Write(make([]byte, 20))
	// Bottom row first: blue, white / red, green.
	dib.Write([]byte{0xff, 0, 0, 0xff, 0xff, 0xff, 0xff, 0xff})
	dib.Write([]byte{0, 0, 0xff, 0xff, 0, 0xff, 0, 0xff})
	writeICO(t, path, dib.Bytes())
}

// writeICOTopDown writes an ico of one 2×2 top-down BMP entry, the height
// doubled or not.
func writeICOTopDown(t *testing.T, path string, doubled bool) {
	t.Helper()
	var dib bytes.Buffer
	binary.Write(&dib, binary.LittleEndian, uint32(40))
	binary.Write(&dib, binary.LittleEndian, int32(2))
	if doubled {
		binary.Write(&dib, binary.LittleEndian, int32(-4))
	} else {
		binary.Write(&dib, binary.LittleEndian, int32(-2))
	}
	binary.Write(&dib, binary.LittleEndian, uint16(1))
	binary.Write(&dib, binary.LittleEndian, uint16(32))
	binary.Write(&dib, binary.LittleEndian, uint32(0))
	dib.Write(make([]byte, 20))
	// Top row first: red, green / blue, white.
	dib.Write([]byte{0, 0, 0xff, 0xff, 0, 0xff, 0, 0xff})
	dib.Write([]byte{0xff, 0, 0, 0xff, 0xff, 0xff, 0xff, 0xff})
	writeICO(t, path, dib.Bytes())
}

// writeICOFallback writes an ico whose larger entry is truncated PNG and
// whose smaller one decodes.
func writeICOFallback(t *testing.T, path string, good []byte) {
	t.Helper()
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, uint16(0))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint16(2))
	bad := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 2048)...)
	b.Write([]byte{32, 32, 0, 0})
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint16(32))
	binary.Write(&b, binary.LittleEndian, uint32(len(bad)))
	binary.Write(&b, binary.LittleEndian, uint32(6+32))
	b.Write([]byte{16, 16, 0, 0})
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint16(32))
	binary.Write(&b, binary.LittleEndian, uint32(len(good)))
	binary.Write(&b, binary.LittleEndian, uint32(6+32+len(bad)))
	b.Write(bad)
	b.Write(good)
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// icoPixels decodes path and returns its pixels row by row.
func icoPixels(t *testing.T, path string) []color.NRGBA {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := decodeICO(data)
	if err != nil {
		t.Fatal(err)
	}
	var out []color.NRGBA
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			out = append(out, color.NRGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)})
		}
	}
	return out
}

// A logo that does not decode leaves the next candidate to try.
func TestLoadProjectLogoSkipsBroken(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "favicon.svg"), []byte("not an svg"), 0o644); err != nil {
		t.Fatal(err)
	}
	if loadProjectLogo(dir) != nil {
		t.Fatal("a logo from a broken svg")
	}
	writePNG(t, filepath.Join(dir, "favicon.png"), 16, 16)
	if loadProjectLogo(dir) == nil {
		t.Error("the png after a broken svg is not tried")
	}
}

// A scan for another folder drops the logo of the folder before.
func TestScanProjectLogoDropsOldLogo(t *testing.T) {
	root := t.TempDir()
	f, hs, _ := railFixture(t, root)
	h := hs[0]
	writePNG(t, filepath.Join(root, "logo.png"), 16, 16)
	h.logoSrc = loadProjectLogo(root)
	if h.logoSrc == nil {
		t.Fatal("no logo to drop")
	}
	f.scanProjectLogo(h.a, h, t.TempDir(), "")
	if h.logoSrc != nil {
		t.Error("the old folder's logo shows while the new folder is scanned")
	}
}

// The rail looks for the logo of every project, one not yet shown too.
func TestRailScansProjectsNotShown(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	writePNG(t, filepath.Join(other, "logo.png"), 16, 16)
	_, hs, tt := railFixture(t, root, other)
	if hs[1].a.started {
		t.Fatal("the second project started")
	}
	step(tt)
	if !hs[1].logoDone || hs[1].logoDir != other {
		t.Fatal("no scan for a project not shown")
	}
	for end := time.Now().Add(settleTimeout); hs[1].logoSrc == nil; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("the logo of a project not shown never came")
		}
		step(tt)
	}
}

// settleFixture is a window of one project with a changed file, whose
// question about it stays open until answered.
func settleFixture(t *testing.T) (*frame, *fakeHost, *ui.Tester) {
	t.Helper()
	root := workspaceFixture(t)
	f, hs, tt := railFixture(t, root)
	// As the app keeps the windows it opens.
	f.m.frames = []*frame{f}
	f.m.s.AutoSave = false
	a := hs[0].a
	a.openFile(filepath.Join(root, "main.go"), true)
	// An edit made before the file is read is not a change to it.
	deadline := time.Now().Add(settleTimeout)
	for ed := a.activeEditor(); (ed == nil || ed.sync == syncLoading) && time.Now().Before(deadline); ed = a.activeEditor() {
		step(tt)
		time.Sleep(5 * time.Millisecond)
	}
	ed := a.activeEditor()
	if ed == nil || ed.sync == syncLoading {
		t.Fatal("main.go did not load")
	}
	ed.edit(ed.sel, textEdit{0, 0, "// changed\n"})
	step(tt)
	host := a.host.(*fakeHost)
	host.holdClose = true
	return f, host, tt
}

// Quitting again while the question about a change shows asks it once,
// so that one Cancel puts the quit off, and the next quit asks again.
func TestQuitTwiceAsksOnce(t *testing.T) {
	f, host, tt := settleFixture(t)
	m := f.m
	for range 2 {
		if m.beforeQuit() {
			t.Fatal("quit with a change not asked about")
		}
		step(tt)
	}
	if len(host.asked) != 1 || len(host.held) != 1 {
		t.Fatalf("asked %v", host.asked)
	}
	host.held[0](2)
	step(tt)
	if m.life != lifeOpen {
		t.Errorf("after Cancel the app is %v", m.life)
	}
	if m.beforeQuit() {
		t.Fatal("quit with a change not asked about")
	}
	step(tt)
	if len(host.asked) != 2 {
		t.Errorf("quitting after Cancel asked %v", host.asked)
	}
}

// Closing the window again while the question about a change shows asks
// it once, as does quitting meanwhile.
func TestCloseTwiceAsksOnce(t *testing.T) {
	f, host, tt := settleFixture(t)
	for range 2 {
		if f.beforeClose() {
			t.Fatal("closed with a change not asked about")
		}
		step(tt)
	}
	if f.m.beforeQuit() {
		t.Fatal("quit with a change not asked about")
	}
	step(tt)
	if len(host.asked) != 1 || len(host.held) != 1 {
		t.Fatalf("asked %v", host.asked)
	}
	host.held[0](2)
	step(tt)
	if f.life != lifeOpen || f.m.life != lifeOpen {
		t.Errorf("after Cancel the window is %v, the app %v", f.life, f.m.life)
	}
}

// Posts and the rail's refresh tick run while the window switches its
// projects, reorders the rail, and closes, without a race: the goroutines
// a timer and background work run on read the frame under its mutex.
func TestRailPostsRaceWithSwitchingAndClosing(t *testing.T) {
	root, other := workspaceFixture(t), workspaceFixture(t)
	f, tabs, tt := railFixture(t, root, other)
	done := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				f.post(func() {})
				f.invalidate()
				f.scheduleRailTick()
				f.railTick()
				// Slow enough that the main thread changes the window
				// between one round and the next.
				time.Sleep(time.Millisecond)
			}
		}()
	}
	for i := range 50 {
		f.activate(tabs[i%2])
		f.move(tabs[i%2], tabs[(i+1)%2])
		step(tt)
	}
	// The window closes as the goroutines may still run, and what is left
	// posted draws nothing.
	f.stop()
	close(done)
	wg.Wait()
	f.mu.Lock()
	closed, target := f.closed, f.target
	f.mu.Unlock()
	if !closed || target != nil {
		t.Errorf("after stop the window is closed=%v, posts go to %v", closed, target)
	}
}

// mayoi projects remove takes the projects off their rail, and closes the
// window a last one leaves without a project, as closing its last tab does.
func TestProjectsRemoveClosesTheLastProjectWindow(t *testing.T) {
	root, other := workspaceFixture(t), workspaceFixture(t)
	f, hs, _ := railFixture(t, root, other)
	m := f.m
	remove := func(dirs ...string) string {
		var said cli.Reply
		var failed string
		m.serveProjects(&cli.Projects{Verb: "remove", Folders: dirs},
			func(r cli.Reply) { said = r },
			func(format string, args ...any) { failed = fmt.Sprintf(format, args...) })
		if failed != "" {
			return failed
		}
		return said.Error
	}
	// One of two tabs goes, and the window keeps the other.
	if got := remove(other); got != "" {
		t.Fatalf("remove said %q", got)
	}
	if len(f.tabs) != 1 || f.active != hs[0] || hs[1].f != nil || len(m.wins) != 1 {
		t.Fatalf("removing one of two projects left %d tabs, %d projects, %v in a window, %v shown", len(f.tabs), len(m.wins), hs[1].f, f.active)
	}
	// The last one closes its window rather than leaving it with none.
	if got := remove(root); got != "" {
		t.Fatalf("remove of the last project said %q", got)
	}
	if len(m.wins) != 0 || !f.closed {
		t.Fatalf("removing the last project left %d projects, closed %v", len(m.wins), f.closed)
	}
}

// A command that names every project of its window takes it apart at once,
// and one naming a project twice leaves the other of its tabs.
func TestProjectsRemoveEveryProject(t *testing.T) {
	root, other := workspaceFixture(t), workspaceFixture(t)
	f, _, _ := railFixture(t, root, other)
	m := f.m
	var said cli.Reply
	var failed string
	m.serveProjects(&cli.Projects{Verb: "remove", Folders: []string{root, root}},
		func(r cli.Reply) { said = r },
		func(format string, args ...any) { failed = fmt.Sprintf(format, args...) })
	if failed != "" || said.Error != "" || len(f.tabs) != 1 || f.active == nil {
		t.Fatalf("removing one of two projects twice: %q %q, %d tabs", failed, said.Error, len(f.tabs))
	}
	root, other = workspaceFixture(t), workspaceFixture(t)
	f, _, _ = railFixture(t, root, other)
	m = f.m
	m.serveProjects(&cli.Projects{Verb: "remove", Folders: []string{root, other}},
		func(r cli.Reply) { said = r },
		func(format string, args ...any) { failed = fmt.Sprintf(format, args...) })
	if failed != "" || said.Error != "" || len(f.tabs) == 0 && !f.closed {
		t.Fatalf("removing every project: %q %q, %d tabs, closed %v", failed, said.Error, len(f.tabs), f.closed)
	}
}

// A folder inside a repository is in one, as a monorepo's own folders are,
// and one outside any is not.
func TestHasRepositoryFindsTheOneAbove(t *testing.T) {
	dir := t.TempDir()
	gitAt(t, dir, time.Time{}, "init", "-q", "-b", "main")
	sub := filepath.Join(dir, "internal", "a")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if !hasRepository(sub) || !hasRepository(dir) {
		t.Error("a folder of a repository is not in one")
	}
	if hasRepository(t.TempDir()) {
		t.Error("a folder outside any repository is in one")
	}
	if hasRepository("host:/srv/thing") {
		t.Error("a remote folder is in one here")
	}
}

// Closing the active project via closeProject drains the drop before building
// the frame and never panics when shown, focused, or setTitle are called on a
// dropped project.
func TestCloseActiveProjectDoesNotCrash(t *testing.T) {
	root, other := workspaceFixture(t), workspaceFixture(t)
	f, tabs, tt := railFixture(t, root, other)
	closed := tabs[0]
	buildsBefore := closed.a.builds
	closed.closeProject()
	step(tt)
	if f.active != tabs[1] || len(f.tabs) != 1 {
		t.Fatalf("after closeProject: active=%v tabs=%d, want %v and 1", f.active, len(f.tabs), tabs[1])
	}
	if closed.f != nil {
		t.Fatalf("closed project still holds frame %v", closed.f)
	}
	if closed.a.builds != buildsBefore {
		t.Errorf("closed project built %d more frame(s) after drop", closed.a.builds-buildsBefore)
	}
	if closed.shown() || closed.focused() {
		t.Error("dropped project reports shown or focused")
	}
	closed.setTitle("ignored")
}

// Dragging or opening an already-open folder (even with a trailing slash or
// through a symlink) focuses the existing project rather than duplicating it.
func TestOpenFolderDeduplicates(t *testing.T) {
	root, other := workspaceFixture(t), workspaceFixture(t)
	f, tabs, _ := railFixture(t, root, other)
	m := f.m
	f.activate(tabs[1])

	// Trailing slash.
	tabs[1].openFolders([]string{root + string(filepath.Separator), root})
	if len(f.tabs) != 2 || len(m.wins) != 2 {
		t.Fatalf("trailing slash duplicated project: %d tabs, %d wins", len(f.tabs), len(m.wins))
	}
	if f.active != tabs[0] {
		t.Errorf("opening existing folder did not focus it: active=%v", f.active)
	}

	// Symlink to the same folder.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(other, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	tabs[0].openFolders([]string{link})
	if len(f.tabs) != 2 || len(m.wins) != 2 {
		t.Fatalf("symlink duplicated project: %d tabs, %d wins", len(f.tabs), len(m.wins))
	}
	if f.active != tabs[1] {
		t.Errorf("opening symlink to existing folder did not focus it: active=%v", f.active)
	}
}

// Opening a non-independent subfolder (no .git) of an already-open Git
// repository focuses that repository instead of opening a duplicate tab,
// while nested independent repos (with .git) and sibling monorepo folders
// when the root is not open still open on their own.
func TestOpenFolderDeduplicatesRepoSubfolders(t *testing.T) {
	repo := workspaceFixture(t)
	gitAt(t, repo, time.Time{}, "init", "-q", "-b", "main")
	subA := filepath.Join(repo, "sub-a")
	subB := filepath.Join(repo, "sub-b")
	nested := filepath.Join(repo, "nested-repo")
	for _, d := range []string{subA, subB, nested} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitAt(t, nested, time.Time{}, "init", "-q", "-b", "main")

	// When repo root is open, dragging sub-a focuses repo instead of duplicating.
	other := workspaceFixture(t)
	f, tabs, _ := railFixture(t, repo, other)
	f.activate(tabs[1])
	tabs[1].openFolders([]string{subA})
	if len(f.tabs) != 2 || f.active != tabs[0] {
		t.Fatalf("subfolder of open repo: tabs=%d active=%v, want 2 and repo", len(f.tabs), f.active)
	}

	// A nested independent repo (with its own .git) still opens separately.
	tabs[0].openFolders([]string{nested})
	if len(f.tabs) != 3 {
		t.Fatalf("nested repo with own .git did not open: tabs=%d", len(f.tabs))
	}

	// When repo root is NOT open, sibling monorepo subfolders can both open.
	f2, tabs2, _ := railFixture(t, subA, other)
	tabs2[0].openFolders([]string{subB})
	if len(f2.tabs) != 3 {
		t.Fatalf("sibling monorepo subfolder did not open when root closed: tabs=%d", len(f2.tabs))
	}
}
