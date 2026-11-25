package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"mayoi/locales"
)

// scanProjectInfo reads a work tree's files as Git lists them, leaving what
// .gitignore names and binaries' lines out, and counts each language.
func TestScanProjectInfoGit(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "remote", "add", "origin", "https://github.com/me/app.git")
	writeText(t, filepath.Join(dir, ".gitignore"), "ignored/\n")
	writeText(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
	writeText(t, filepath.Join(dir, "util.go"), "package main\n\nfunc util() {}\n")
	writeText(t, filepath.Join(dir, "notes.md"), "# Notes\n\ntext\n")
	writeText(t, filepath.Join(dir, "ignored", "blob.go"), strings.Repeat("x\n", 40))
	if err := os.WriteFile(filepath.Join(dir, "image.bin"), []byte("PK\x00\x00\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-qm", "the first")

	info, err := scanProjectInfo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsGit || info.Branch != "main" || info.Remote != "github.com/me/app" {
		t.Fatalf("git %+v", info)
	}
	if info.Commits != 1 || info.Branches != 1 || info.Contributors != 1 {
		t.Fatalf("commits %d, branches %d, contributors %d", info.Commits, info.Branches, info.Contributors)
	}
	if info.First.IsZero() || info.Last.IsZero() {
		t.Fatalf("dates %v %v", info.First, info.Last)
	}
	// Five files: .gitignore, the two Go files, the note, and the binary,
	// which is sized but its lines left out. Ignored/blob.go is not there.
	if info.Files != 5 || info.Lines != 10 || info.NonBlank != 7 {
		t.Fatalf("%d files, %d lines, %d of them not blank, want 5, 10 and 7", info.Files, info.Lines, info.NonBlank)
	}
	var goLang, other projectLang
	for _, l := range info.Langs {
		switch l.Name {
		case "Go":
			goLang = l
		case "Other":
			other = l
		}
	}
	if goLang.Files != 2 || goLang.Lines != 6 || goLang.Color == "" {
		t.Fatalf("Go: %+v", goLang)
	}
	if other.Files != 1 || other.Lines != 1 {
		t.Fatalf("Other: %+v", other)
	}
	if len(info.Langs) != 3 || info.Langs[0].Name != "Go" {
		t.Fatalf("languages %+v", info.Langs)
	}

	// A file changed after the commit shows in the working tree.
	writeText(t, filepath.Join(dir, "notes.md"), "# Notes\n\ntext\nanother\n")
	info, err = scanProjectInfo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Changed != 1 {
		t.Fatalf("%d changed files, want 1", info.Changed)
	}
}

// scanProjectInfo walks a folder that is not a work tree, leaving what
// builds aside.
func TestScanProjectInfoPlainFolder(t *testing.T) {
	dir := t.TempDir()
	writeText(t, filepath.Join(dir, "app.py"), "print(1)\n\nprint(2)\n")
	writeText(t, filepath.Join(dir, "notes.txt"), "hello\n")
	for _, build := range []string{"node_modules", "build", ".git"} {
		if err := os.MkdirAll(filepath.Join(dir, build), 0o755); err != nil {
			t.Fatal(err)
		}
		writeText(t, filepath.Join(dir, build, "generated.py"), strings.Repeat("x\n", 20))
	}
	info, err := scanProjectInfo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.IsGit || info.Files != 2 || info.Lines != 4 {
		t.Fatalf("git %v, %d files, %d lines, want 2 and 4", info.IsGit, info.Files, info.Lines)
	}
	if len(info.Langs) != 2 || info.Langs[0].Name != "Python" || info.Langs[0].Lines != 3 {
		t.Fatalf("languages %+v", info.Langs)
	}
}

// A repository without a commit reads as one, with no commit to name and
// no line to fear.
func TestScanProjectInfoUnborn(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	writeText(t, filepath.Join(dir, "main.go"), "package main\n")
	info, err := scanProjectInfo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsGit || info.Commits != 0 || !info.First.IsZero() || !info.Last.IsZero() {
		t.Fatalf("unborn: %+v", info)
	}
	if info.Changed != 1 {
		t.Fatalf("%d changed files, want the untracked one", info.Changed)
	}
}

// Many languages list the eight with the most lines, and the rest as
// Other.
func TestProjectInfoFoldsLanguages(t *testing.T) {
	dir := t.TempDir()
	names := []struct{ file, ext string }{
		{"a", "go"}, {"b", "ts"}, {"c", "py"}, {"d", "rs"}, {"e", "java"},
		{"f", "kt"}, {"g", "swift"}, {"h", "rb"}, {"i", "php"}, {"j", "lua"},
	}
	for i, n := range names {
		writeText(t, filepath.Join(dir, n.file+"."+n.ext), strings.Repeat("x\n", i+1))
	}
	info, err := scanProjectInfo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Langs) != 9 {
		t.Fatalf("%d languages, want 9", len(info.Langs))
	}
	if last := info.Langs[len(info.Langs)-1]; last.Name != "Other" {
		t.Fatalf("last language %q, want Other", last.Name)
	} else if last.Files != 2 {
		t.Fatalf("Other has %d files, want the two past the eight", last.Files)
	}
}

// The dialog shows the numbers, the languages and the repository of the
// project, and Escape shuts it.
func TestProjectInfoDialog(t *testing.T) {
	hs := newHarness(t, false)
	hs.gitRepo()
	a := hs.a
	a.openProjectInfo(a.root, "Mayoi")
	hs.settle()
	if !a.projectInfo.open || a.projectInfo.data == nil {
		t.Fatalf("dialog open %v, data %+v", a.projectInfo.open, a.projectInfo.data)
	}
	if !hs.tt.HasText(locales.ProjectInfoTitle()) || !hs.tt.HasText(locales.ProjectInfoLines()) {
		t.Fatalf("the dialog is not shown: %q", hs.tt.Texts())
	}
	if !hs.tt.HasText(locales.ProjectInfoLanguages()) || !hs.tt.HasText(locales.ProjectInfoRepository()) {
		t.Fatalf("no languages or repository: %q", hs.tt.Texts())
	}
	if !hs.tt.HasText(locales.Stats24h()) || !hs.tt.HasText(locales.Stats7d()) || !hs.tt.HasText(locales.Stats30d()) {
		t.Fatalf("no 24h/7d/30d stats: %q", hs.tt.Texts())
	}
	hs.tt.Key(0, ui.KeyEscape)
	hs.settle()
	if a.projectInfo.open {
		t.Fatal("Escape left the dialog open")
	}
}

func writeText(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
