package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mayoi/internal/bg"
	"mayoi/internal/remote"
	"mayoi/internal/vfs"
	"mayoi/locales"
)

// videoExts are the video formats the media viewer previews.
var videoExts = map[string]bool{
	".mp4":  true,
	".m4v":  true,
	".mov":  true,
	".webm": true,
	".ogv":  true,
	".mkv":  true,
	".avi":  true,
}

// isVideo reports whether a file is a video the viewer previews, by its
// extension.
func isVideo(path string) bool { return videoExts[strings.ToLower(filepath.Ext(path))] }

// isMedia reports whether a file is an image or a video.
func isMedia(path string) bool { return isImage(path) || isVideo(path) }

func videoMIME(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".ogv":
		return "video/ogg"
	case ".mkv":
		return "video/x-matroska"
	case ".avi":
		return "video/x-msvideo"
	}
	return "application/octet-stream"
}

// mediaEntry is a local file or in-memory blob served to an embedded
// video WebView over localhost, supporting HTTP Range seeking.
type mediaEntry struct {
	path string
	name string
	data []byte
	mod  time.Time
}

var mediaServer = struct {
	once    sync.Once
	baseURL string
	token   string
	err     error

	mu      sync.RWMutex
	entries map[string]mediaEntry
	nextID  atomic.Uint64
}{
	entries: map[string]mediaEntry{},
}

func startMediaServer() (string, error) {
	mediaServer.once.Do(func() {
		var tok [16]byte
		if _, err := rand.Read(tok[:]); err != nil {
			mediaServer.err = err
			return
		}
		mediaServer.token = hex.EncodeToString(tok[:])
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			mediaServer.err = err
			return
		}
		mediaServer.baseURL = "http://" + ln.Addr().String() + "/media/" + mediaServer.token + "/"
		mux := http.NewServeMux()
		prefix := "/media/" + mediaServer.token + "/"
		mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
			id := strings.TrimPrefix(r.URL.Path, prefix)
			mediaServer.mu.RLock()
			entry, ok := mediaServer.entries[id]
			mediaServer.mu.RUnlock()
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			if entry.path != "" {
				if ct := videoMIME(entry.path); ct != "application/octet-stream" {
					w.Header().Set("Content-Type", ct)
				}
				http.ServeFile(w, r, entry.path)
				return
			}
			if ct := videoMIME(entry.name); ct != "application/octet-stream" {
				w.Header().Set("Content-Type", ct)
			}
			http.ServeContent(w, r, entry.name, entry.mod, bytes.NewReader(entry.data))
		})
		srv := &http.Server{Handler: mux}
		go srv.Serve(ln)
	})
	return mediaServer.baseURL, mediaServer.err
}

func registerMediaFile(path string) (string, string, error) {
	base, err := startMediaServer()
	if err != nil {
		return "", "", err
	}
	id := strconv.FormatUint(mediaServer.nextID.Add(1), 36)
	mediaServer.mu.Lock()
	mediaServer.entries[id] = mediaEntry{path: path}
	mediaServer.mu.Unlock()
	return id, base + id, nil
}

func registerMediaBytes(name string, data []byte) (string, string, error) {
	base, err := startMediaServer()
	if err != nil {
		return "", "", err
	}
	id := strconv.FormatUint(mediaServer.nextID.Add(1), 36)
	mediaServer.mu.Lock()
	mediaServer.entries[id] = mediaEntry{name: name, data: data, mod: time.Now()}
	mediaServer.mu.Unlock()
	return id, base + id, nil
}

func unregisterMedia(id string) {
	if id == "" {
		return
	}
	mediaServer.mu.Lock()
	delete(mediaServer.entries, id)
	mediaServer.mu.Unlock()
}

// videoPlayer holds the state and native WebView of an embedded video
// preview, used both in standalone video tabs and in diff views.
type videoPlayer struct {
	mediaID  string
	url      string
	wv       *mygo.WebView
	bgHex    string
	w, h     int
	duration float64
	err      string
}

func (vp *videoPlayer) stop() {
	if vp == nil {
		return
	}
	if vp.wv != nil {
		vp.wv.Destroy()
		vp.wv = nil
	}
	if vp.mediaID != "" {
		unregisterMedia(vp.mediaID)
		vp.mediaID = ""
	}
	vp.url = ""
}

var videoPageTmpl = template.Must(template.New("video").Parse(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<style>
html, body {
  margin: 0;
  padding: 0;
  width: 100%;
  height: 100%;
  overflow: hidden;
  background: {{.BG}};
  display: flex;
  align-items: center;
  justify-content: center;
}
video {
  max-width: 100%;
  max-height: 100%;
  width: auto;
  height: auto;
  outline: none;
  background: transparent;
}
</style>
</head>
<body>
<video id="v" controls playsinline preload="metadata" src="{{.URL}}"></video>
<script>
(function() {
  const v = document.getElementById('v');
  function post(msg) {
    window.location.href = 'mayoi-message:' + encodeURIComponent(JSON.stringify(msg));
  }
  v.addEventListener('loadedmetadata', function() {
    post({ w: v.videoWidth || 0, h: v.videoHeight || 0, duration: isFinite(v.duration) ? v.duration : 0 });
  });
  v.addEventListener('error', function() {
    const code = v.error ? v.error.code : 0;
    post({ err: code ? ('media error ' + code) : 'unsupported video format' });
  });
})();
</script>
</body>
</html>`))

func videoPlayerHTML(mediaURL, bgHex string) string {
	var buf bytes.Buffer
	_ = videoPageTmpl.Execute(&buf, struct {
		URL string
		BG  string
	}{
		URL: mediaURL,
		BG:  bgHex,
	})
	return buf.String()
}

// ensureWebView creates or updates the native WebView for this video
// player on a desktop window. Returns nil in headless test environments.
func (vp *videoPlayer) ensureWebView(a *app, bgHex string) *mygo.WebView {
	if vp == nil || vp.url == "" {
		return nil
	}
	h, ok := a.host.(*desktop)
	if !ok || h.f == nil || h.f.win == nil {
		return nil
	}
	if vp.wv != nil && !vp.wv.IsDestroyed() {
		if vp.bgHex != bgHex {
			vp.bgHex = bgHex
			_ = vp.wv.SetBackgroundColor(bgHex)
			_, _ = vp.wv.Page().Eval(fmt.Sprintf("document.documentElement.style.background=%q;document.body.style.background=%q;", bgHex, bgHex))
		}
		return vp.wv
	}
	wv, err := h.f.win.NewWebView(mygo.WebViewOptions{BackgroundColor: bgHex})
	if err != nil {
		vp.err = err.Error()
		return nil
	}
	vp.wv, vp.bgHex = wv, bgHex
	pg := wv.Page()
	pg.OnWillNavigate(func(e *mygo.NavigateEvent) {
		data, ok := strings.CutPrefix(e.URL, "mayoi-message:")
		if !ok {
			return
		}
		e.PreventDefault()
		raw, err := url.PathUnescape(data)
		if err != nil {
			return
		}
		var msg struct {
			W        int     `json:"w"`
			H        int     `json:"h"`
			Duration float64 `json:"duration"`
			Err      string  `json:"err"`
		}
		if json.Unmarshal([]byte(raw), &msg) != nil {
			return
		}
		a.post(func() {
			if msg.Err != "" {
				vp.err = msg.Err
			} else {
				vp.err = ""
				vp.w, vp.h, vp.duration = msg.W, msg.H, msg.Duration
			}
		})
	})
	pg.LoadHTML(videoPlayerHTML(vp.url, bgHex), "")
	return wv
}

// newVideoViewer makes the viewer of a video file, which loadVideo reads.
func newVideoViewer(path string) *viewer {
	return &viewer{path: path, video: &videoPlayer{}}
}

// loadVideo prepares a video file for playback in the background.
// Local files are streamed straight from disk via the local HTTP media
// server without loading the full file into memory.
func (a *app) loadVideo(v *viewer) {
	v.loading++
	n, path := v.loading, v.path
	bg.Go(func() {
		defer recoverCrash()
		var id, mediaURL string
		var size int64
		var err error
		if remote.Is(path) {
			var data []byte
			data, err = vfs.ReadFileLimit(path, maxOpenBytes)
			if errors.Is(err, remote.ErrTooLarge) {
				err = errors.New(locales.ErrVideoTooLarge())
			}
			if err == nil {
				size = int64(len(data))
				id, mediaURL, err = registerMediaBytes(filepath.Base(path), data)
			}
		} else {
			var info os.FileInfo
			info, err = os.Stat(path)
			if err == nil && !info.Mode().IsRegular() {
				err = errors.New(locales.ErrImageNotRegular())
			}
			if err == nil {
				size = info.Size()
				id, mediaURL, err = registerMediaFile(path)
			}
		}
		a.post(func() {
			if v.loading != n {
				unregisterMedia(id)
				return
			}
			v.loaded = true
			if err != nil {
				unregisterMedia(id)
				v.err = err.Error()
				return
			}
			v.err, v.size = "", size
			if v.video == nil {
				v.video = &videoPlayer{}
			}
			v.video.stop()
			v.video.err = ""
			v.video.w, v.video.h, v.video.duration = 0, 0, 0
			v.video.mediaID, v.video.url = id, mediaURL
		})
	})
}

func formatDuration(sec float64) string {
	if sec <= 0 || math.IsNaN(sec) || math.IsInf(sec, 0) {
		return ""
	}
	total := int(math.Round(sec))
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// videoView is the pane of a video viewer: an embedded HTML5 player on
// desktop windows, or a quiet summary card when headless or on error.
func (a *app) videoView(c *ui.Context, v *viewer, name string) {
	p := a.pal
	pane := ui.Box(c.Key("video-viewer")).Bind(&v.pane).Fill().Background(p.editor).Clip().Focusable().FocusRing(false).
		Label(locales.VideoLabel(name))
	pane.Children(func() {
		if !v.loaded {
			return
		}
		errMsg := v.err
		if errMsg == "" && v.video != nil {
			errMsg = v.video.err
		}
		if errMsg != "" {
			ui.Column(c).Fill().Center().Padding(24).Gap(10).Children(func() {
				ui.Icon(c, iconVideo).FontSize(32).TextColor(p.faint)
				ui.Text(c, locales.CannotShowWhy(name, errMsg)).FontSize(13).TextColor(p.muted).TextAlign(ui.Center).MaxWidth(420)
				if v.path != "" && !remote.Is(v.path) {
					if ui.Button(c, revealLabel()).Clicked() {
						a.host.reveal(v.path)
					}
				}
			})
			return
		}
		if wv := v.video.ensureWebView(a, hexColor(p.editor)); wv != nil {
			ui.WebView(c.Key("video:"+v.video.mediaID), wv).Fill()
			return
		}
		// Headless environment (tests): show a quiet summary of the video.
		ui.Column(c).Fill().Center().Padding(24).Gap(8).Children(func() {
			ui.Icon(c, iconVideo).FontSize(32).TextColor(p.faint)
			ui.Text(c, name).FontSize(13).FontWeight(500).TextColor(p.text)
			if v.size > 0 {
				ui.Text(c, sizeText(v.size)).FontSize(12).TextColor(p.muted)
			}
		})
	})
}
