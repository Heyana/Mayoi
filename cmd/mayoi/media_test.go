package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestVideoOpensInAViewer(t *testing.T) {
	hs := newHarness(t, true)
	a, tt := hs.a, hs.tt
	path := hs.path("clip.mp4")
	payload := []byte("0123456789abcdef_fake_mp4_stream_bytes")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	a.openFile(path, true)
	hs.until("video loaded", func() bool {
		v := a.activeViewer()
		return v != nil && v.loaded && v.video != nil && v.video.url != ""
	})
	v := a.activeViewer()
	if a.activeEditor() != nil || len(a.docs) != 0 {
		t.Fatalf("expected video viewer, got editor=%v docs=%d", a.activeEditor(), len(a.docs))
	}
	if v.err != "" || v.size != int64(len(payload)) {
		t.Fatalf("err=%q size=%d want %d", v.err, v.size, len(payload))
	}
	if !tt.HasText("clip.mp4") {
		t.Errorf("video tab missing name in %q", tt.Texts())
	}

	// Local media server serves HTTP Range requests for seeking.
	req, err := http.NewRequest(http.MethodGet, v.video.url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-9")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || string(body) != "0123456789" {
		t.Fatalf("range status=%d body=%q", resp.StatusCode, string(body))
	}

	// Closing the tab unregisters the media URL.
	mediaURL := v.video.url
	a.dropTab(a.activeTab())
	resp2, err := http.Get(mediaURL)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after tab close, got %d", resp2.StatusCode)
	}
}

func TestDiffPreviewsImagesAndVideos(t *testing.T) {
	hs := newHarness(t, true)
	git := hs.gitRepo()
	a := hs.a

	// Commit an initial image so we can test modified image diffs too.
	modImg := hs.path("banner.png")
	writePNG(t, modImg, 32, 24)
	git("add", "banner.png")
	git("commit", "-q", "-m", "add banner")

	// Modify banner.png, add untracked image, untracked video, and non-media binary.
	writePNG(t, modImg, 64, 48)
	newImg := hs.path("shot.png")
	writePNG(t, newImg, 40, 30)
	newVid := hs.path("demo.mp4")
	if err := os.WriteFile(newVid, []byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	binFile := hs.path("data.bin")
	if err := os.WriteFile(binFile, []byte{0x00, 0x01, 0x02, 0x03}, 0o644); err != nil {
		t.Fatal(err)
	}

	a.settings.DiffFiles = "all"
	a.run("view.scm")
	hs.settleGit()
	a.repository().showChanges(diffTarget{path: newImg, any: true}, true)
	hs.until("diff media loaded", func() bool {
		v := a.columnTabDiff()
		if v == nil || len(v.files) < 4 {
			return false
		}
		for _, f := range v.files {
			if !f.loaded {
				return false
			}
		}
		return true
	})

	v := a.columnTabDiff()
	find := func(name string) *diffFile {
		i := slices.IndexFunc(v.files, func(f *diffFile) bool { return filepath.Base(f.path) == name })
		if i < 0 {
			t.Fatalf("%s not in diff files", name)
		}
		return v.files[i]
	}

	// Untracked image has decoded new bitmap and a rowMedia row.
	fImg := find("shot.png")
	if !fImg.binary || fImg.media == nil || fImg.media.kind != diffMediaImage {
		t.Fatalf("shot.png: binary=%v media=%+v", fImg.binary, fImg.media)
	}
	if fImg.media.old.ok || !fImg.media.new.ok || fImg.media.new.bm == nil || fImg.media.new.w != 40 || fImg.media.new.h != 30 {
		t.Fatalf("shot.png media sides: old=%+v new=%+v", fImg.media.old, fImg.media.new)
	}

	// Modified image has both old and new bitmaps decoded.
	fMod := find("banner.png")
	if fMod.media == nil || fMod.media.kind != diffMediaImage || !fMod.media.old.ok || !fMod.media.new.ok {
		t.Fatalf("banner.png media=%+v", fMod.media)
	}
	if fMod.media.old.w != 32 || fMod.media.new.w != 64 {
		t.Fatalf("banner.png dims old=%dx%d new=%dx%d", fMod.media.old.w, fMod.media.old.h, fMod.media.new.w, fMod.media.new.h)
	}

	// Untracked video has diffMediaVideo backed by disk path (not read into RAM).
	fVid := find("demo.mp4")
	if !fVid.binary || fVid.media == nil || fVid.media.kind != diffMediaVideo {
		t.Fatalf("demo.mp4: binary=%v media=%+v", fVid.binary, fVid.media)
	}
	if fVid.media.new.path != newVid || fVid.new.text != "" {
		t.Fatalf("demo.mp4 should stream from disk path, got path=%q textLen=%d", fVid.media.new.path, len(fVid.new.text))
	}

	// Non-media binary still has media == nil and falls back to rowNote.
	fBin := find("data.bin")
	if !fBin.binary || fBin.media != nil {
		t.Fatalf("data.bin: binary=%v media=%+v", fBin.binary, fBin.media)
	}

	// Verify row kinds in layout.
	hasRow := func(f *diffFile, kind rowKind) bool {
		idx := slices.Index(v.files, f)
		for _, r := range v.rows {
			if r.file == idx && r.kind == kind {
				return true
			}
		}
		return false
	}
	if !hasRow(fImg, rowMedia) || !hasRow(fMod, rowMedia) || !hasRow(fVid, rowMedia) {
		t.Fatalf("expected rowMedia for image and video files")
	}
	if !hasRow(fBin, rowNote) {
		t.Fatalf("expected rowNote for non-media binary file")
	}
}
