package main

import (
	"fmt"
	"os"
	"testing"
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
