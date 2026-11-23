package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"mayoi/internal/git"
)

// A diff of one Markdown file shows that file rendered, as reading does,
// and its lines again when asked.
func TestDiffPreviewsTheOneMarkdownFile(t *testing.T) {
	hs := newHarness(t, false)
	hs.gitRepo()
	md := hs.path("notes.md")
	text := "# A title\n\nSome prose in a paragraph, long enough to be one block.\n\n- a bullet\n- another\n"
	if err := os.WriteFile(md, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	hs.settleGit()
	hs.a.showChanges(diffTarget{path: md, any: true}, true)
	hs.settle()
	v := hs.a.activeTab().diff
	if v == nil {
		t.Fatal("no diff opened")
	}
	if v.preview || previewFile(v) == nil {
		t.Fatalf("a Markdown file of its own shows its lines: preview=%v file=%+v", v.preview, previewFile(v))
	}
	hs.a.toggleDiffPreview(v)
	hs.settle()
	if !v.preview || v.markdown == nil || v.markdown.markdown == nil {
		t.Fatalf("the preview did not open: %v %v", v.preview, v.markdown)
	}
	if n := len(v.markdown.markdown.blocks); n == 0 {
		t.Fatal("the preview parsed no blocks")
	} else {
		fmt.Println("blocks", n)
	}
	hs.shot("diff-preview")
	hs.a.toggleDiffPreview(v)
	hs.settle()
	if v.preview || v.markdown != nil {
		t.Fatal("the preview stayed")
	}
}

// A diff that shows one file at a time previews the file it shows, among
// the others it lists.
func TestDiffPreviewsTheFileItShows(t *testing.T) {
	hs := newHarness(t, false)
	hs.a.settings.DiffFiles = "one"
	hs.gitRepo()
	os.WriteFile(hs.path("main.go"), []byte("package main\n"), 0o644)
	os.WriteFile(hs.path("a.md"), []byte("# A\n\ntext\n"), 0o644)
	os.WriteFile(hs.path("b.txt"), []byte("b\n"), 0o644)
	hs.settleGit()
	md := hs.path("a.md")
	hs.a.showChanges(diffTarget{path: md, base: diffIndex}, true)
	hs.settle()
	v := hs.a.activeTab().diff
	if v == nil {
		t.Fatal("no diff opened")
	}
	if len(v.files) < 2 {
		t.Fatalf("%d files listed", len(v.files))
	}
	f := previewFile(v)
	if f == nil || f.path != md {
		t.Fatalf("the file shown previews as %v", f)
	}
	hs.shot("diff-preview-button")
	hs.a.toggleDiffPreview(v)
	hs.settle()
	if v.markdown == nil || v.markdown.doc.path != md {
		t.Fatalf("the preview is of %v", v.markdown)
	}
	hs.shot("diff-preview-oneshot")
}

// A diff of many files has no file to preview, and the toggle leaves it
// as it is.
func TestDiffPreviewsOnlyOneFile(t *testing.T) {
	hs := newHarness(t, false)
	hs.gitRepo()
	for _, name := range []string{"a.md", "b.md"} {
		os.WriteFile(hs.path(name), []byte("# "+name+"\n"), 0o644)
	}
	hs.settleGit()
	hs.a.showChanges(diffTarget{base: diffIndex}, true)
	hs.settle()
	v := hs.a.activeTab().diff
	if v == nil {
		t.Fatal("no diff opened")
	}
	if previewFile(v) != nil {
		t.Fatalf("two files preview as none: %d", len(v.files))
	}
	hs.a.toggleDiffPreview(v)
	hs.settle()
	if v.preview {
		t.Fatal("a preview opened over two files")
	}
}

// A preview of a commit shows the file as the commit has it, not as the
// document open beside the diff, and it keeps up as the diff reads again.
func TestDiffPreviewReadsTheCommitNotTheOpenDocument(t *testing.T) {
	hs := newHarness(t, false)
	gitCmd := hs.gitRepo()
	md := hs.path("notes.md")
	if err := os.WriteFile(md, []byte("# Old\n\nold text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd("add", "notes.md")
	gitCmd("commit", "-qm", "old notes")
	hash := strings.TrimSpace(gitCmd("rev-parse", "HEAD"))
	if err := os.WriteFile(md, []byte("# New\n\nnew text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hs.settleGit()
	// The document of the work tree holds what the working copy has.
	hs.a.previewFile(md)
	hs.settle()
	hs.a.showCommit(git.Commit{Hash: hash}, true)
	hs.settleDiff(hs.a.activeTab().diff)
	v := hs.a.activeTab().diff
	hs.a.toggleDiffPreview(v)
	hs.settle()
	if v.markdown == nil || v.markdown.doc == nil {
		t.Fatal("the preview did not open")
	}
	if got := v.markdown.doc.buf.String(); !strings.Contains(got, "# Old") || strings.Contains(got, "# New") {
		t.Fatalf("the preview shows %q, want the commit's text", got)
	}
	// The preview of a file of the work tree reads it again as it changes.
	hs.a.showChanges(diffTarget{path: md, base: diffIndex}, true)
	hs.settle()
	v = hs.a.activeTab().diff
	hs.a.toggleDiffPreview(v)
	hs.settle()
	if v.markdown.doc == nil || !strings.Contains(v.markdown.doc.buf.String(), "# New") {
		t.Fatalf("the preview is of %v", v.markdown)
	}
	if err := os.WriteFile(md, []byte("# Newer\n\nnewer text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := previewFile(v)
	if f == nil {
		t.Fatal("no file to preview")
	}
	f.gitStale = true
	hs.a.refreshDiff(v)
	hs.settleGit()
	if got := v.markdown.doc.buf.String(); !strings.Contains(got, "# Newer") {
		t.Fatalf("the preview kept %q after the diff read again", got)
	}
}
