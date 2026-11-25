package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"mayoi/internal/bg"
	"mayoi/internal/git"
	"mayoi/internal/remote"
	"mayoi/locales"
)

// projectInfo is what a project is, its files and the lines in them, and
// what its Git repository holds.
type projectInfo struct {
	Dir, Name string
	// Lines are the lines of the files that are text, NonBlank those of
	// them with something on, and Files every file the project lists, which
	// Size is their size in bytes.
	Lines, NonBlank, Files int
	Size                   int64
	IsGit                  bool
	Branch                 string
	Remote                 string
	Commits                int
	Branches               int
	Contributors           int
	Changed                int
	First, Last            time.Time
	// Recent holds the project's commits and line changes over the last 24h,
	// 7d, and 30d, matching the Recent Commits panel.
	Recent [statSpans]statsAmount
	// Langs are the languages of the text files, most lines first.
	Langs []projectLang
}

// projectLang is one language of a project: its text files, the lines in
// them, and the color it shows by.
type projectLang struct {
	Name  string
	Color string
	Files int
	Lines int
}

// projectInfoMaxLines is the size past which a file's lines are not
// counted: a minified bundle or a generated file is a few lines of a
// megabyte, and reading it again for every project does not pay.
const projectInfoMaxLines = 4 << 20

// projectInfoHead is how much of a file is read to tell whether it is
// text: a NUL byte in it says it is not.
const projectInfoHead = 8 << 10

// projectInfoWorkers is how many files are read at once: at least four,
// and as many as the machine's cores make useful.
var projectInfoWorkers = max(4, runtime.NumCPU())

// ignoreDirs are the folders a plain folder's walk leaves out: what
// builds and installs, and what an editor keeps.
var ignoreDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true,
	"target": true, ".next": true, ".nuxt": true, ".venv": true, "venv": true,
	"__pycache__": true, ".idea": true, ".vscode": true,
}

// projectLanguages maps a file's name or extension to its language and
// color, the ones people know them by.
var projectLanguages = map[string]projectLang{
	"go":         {"Go", "#00ADD8", 0, 0},
	"ts":         {"TypeScript", "#3178C6", 0, 0},
	"tsx":        {"TypeScript", "#3178C6", 0, 0},
	"mts":        {"TypeScript", "#3178C6", 0, 0},
	"cts":        {"TypeScript", "#3178C6", 0, 0},
	"js":         {"JavaScript", "#E8C547", 0, 0},
	"jsx":        {"JavaScript", "#E8C547", 0, 0},
	"mjs":        {"JavaScript", "#E8C547", 0, 0},
	"cjs":        {"JavaScript", "#E8C547", 0, 0},
	"py":         {"Python", "#3776AB", 0, 0},
	"rs":         {"Rust", "#DEA584", 0, 0},
	"c":          {"C", "#6E7C86", 0, 0},
	"h":          {"C", "#6E7C86", 0, 0},
	"cc":         {"C++", "#00599C", 0, 0},
	"cpp":        {"C++", "#00599C", 0, 0},
	"cxx":        {"C++", "#00599C", 0, 0},
	"hpp":        {"C++", "#00599C", 0, 0},
	"hh":         {"C++", "#00599C", 0, 0},
	"hxx":        {"C++", "#00599C", 0, 0},
	"cs":         {"C#", "#512BD4", 0, 0},
	"java":       {"Java", "#B07219", 0, 0},
	"kt":         {"Kotlin", "#A97BFF", 0, 0},
	"kts":        {"Kotlin", "#A97BFF", 0, 0},
	"swift":      {"Swift", "#F05138", 0, 0},
	"m":          {"Objective-C", "#438EFF", 0, 0},
	"mm":         {"Objective-C", "#438EFF", 0, 0},
	"html":       {"HTML", "#E34F26", 0, 0},
	"htm":        {"HTML", "#E34F26", 0, 0},
	"css":        {"CSS", "#1572B6", 0, 0},
	"scss":       {"SCSS", "#C6538C", 0, 0},
	"sass":       {"SCSS", "#C6538C", 0, 0},
	"vue":        {"Vue", "#41B883", 0, 0},
	"svelte":     {"Svelte", "#FF3E00", 0, 0},
	"json":       {"JSON", "#7B8794", 0, 0},
	"yml":        {"YAML", "#CB171E", 0, 0},
	"yaml":       {"YAML", "#CB171E", 0, 0},
	"toml":       {"TOML", "#9C4221", 0, 0},
	"xml":        {"XML", "#0060AC", 0, 0},
	"md":         {"Markdown", "#083FA1", 0, 0},
	"markdown":   {"Markdown", "#083FA1", 0, 0},
	"sh":         {"Shell", "#89E051", 0, 0},
	"bash":       {"Shell", "#89E051", 0, 0},
	"zsh":        {"Shell", "#89E051", 0, 0},
	"fish":       {"Shell", "#89E051", 0, 0},
	"sql":        {"SQL", "#E38C00", 0, 0},
	"dockerfile": {"Dockerfile", "#2496ED", 0, 0},
	"makefile":   {"Makefile", "#427819", 0, 0},
	"mk":         {"Makefile", "#427819", 0, 0},
	"proto":      {"Protobuf", "#4A86E8", 0, 0},
	"graphql":    {"GraphQL", "#E10098", 0, 0},
	"gql":        {"GraphQL", "#E10098", 0, 0},
	"lua":        {"Lua", "#2C2D72", 0, 0},
	"rb":         {"Ruby", "#CC342D", 0, 0},
	"php":        {"PHP", "#777BB4", 0, 0},
	"dart":       {"Dart", "#0175C2", 0, 0},
	"scala":      {"Scala", "#DC322F", 0, 0},
	"r":          {"R", "#276DC3", 0, 0},
	"ex":         {"Elixir", "#6E4A7E", 0, 0},
	"exs":        {"Elixir", "#6E4A7E", 0, 0},
	"hs":         {"Haskell", "#5D4F85", 0, 0},
	"zig":        {"Zig", "#F7A41D", 0, 0},
	"nix":        {"Nix", "#7E7EFF", 0, 0},
}

// projectInfoOther is the language of the text files none of the above
// names: a lock file, a plain list, a config of some other tool.
var projectInfoOther = projectLang{Name: "Other", Color: "#8B95A7"}

// scanProjectInfo reads what dir is: its files and their lines, its
// languages, and what its Git repository holds, or only the first for a
// plain folder.
func scanProjectInfo(dir string) (*projectInfo, error) {
	if dir == "" {
		return nil, fmt.Errorf("no folder")
	}
	info := &projectInfo{Dir: dir, Name: filepath.Base(dir)}
	var files []string
	if repo, err := git.Open(dir); err == nil {
		info.IsGit = true
		info.Name = filepath.Base(repo.Root)
		files, err = listGitFiles(repo.Root)
		if err != nil {
			return nil, err
		}
		readGitInfo(info, repo.Root)
	} else {
		var err error
		files, err = walkProjectFiles(dir)
		if err != nil {
			return nil, err
		}
	}
	countProjectFiles(info, files)
	return info, nil
}

// listGitFiles lists the files Git keeps and does not ignore, which leaves
// .git and what .gitignore names out without reading either.
func listGitFiles(root string) ([]string, error) {
	out, err := gitRead(root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			files = append(files, filepath.Join(root, filepath.FromSlash(p)))
		}
	}
	return files, nil
}

// walkProjectFiles lists the files of a folder that is not a work tree,
// leaving out what builds and installs, and the editor's own folders.
func walkProjectFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // a folder gone as it walks, as a build's, is left out
		}
		name := d.Name()
		if d.IsDir() {
			if path != dir && (ignoreDirs[name] || name == ".DS_Store") {
				return filepath.SkipDir
			}
			return nil
		}
		if name == ".DS_Store" || !d.Type().IsRegular() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	return files, err
}

// projectFile is what reading one file found.
type projectFile struct {
	size     int64
	lines    int
	nonBlank int
	lang     *projectLang
}

// countProjectFiles totals the files of a project and what is in them: a
// few files at a time, as reading them is the slow part of a project of
// thousands.
func countProjectFiles(info *projectInfo, files []string) {
	if len(files) == 0 {
		return
	}
	var mu sync.Mutex
	var size int64
	var lines, nonBlank int
	langs := map[string]*projectLang{}
	jobs := make(chan string)
	var wg sync.WaitGroup
	for range projectInfoWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				st, ok := inspectProjectFile(path)
				if !ok {
					continue
				}
				mu.Lock()
				size += st.size
				lines += st.lines
				nonBlank += st.nonBlank
				if st.lang != nil {
					l := langs[st.lang.Name]
					if l == nil {
						l = &projectLang{Name: st.lang.Name, Color: st.lang.Color}
						langs[st.lang.Name] = l
					}
					l.Files++
					l.Lines += st.lines
				}
				mu.Unlock()
			}
		}()
	}
	for _, path := range files {
		jobs <- path
	}
	close(jobs)
	wg.Wait()
	info.Size, info.Lines, info.NonBlank = size, lines, nonBlank
	info.Files = len(files)
	for _, l := range langs {
		if l.Lines > 0 {
			info.Langs = append(info.Langs, *l)
		}
	}
	sort.Slice(info.Langs, func(i, j int) bool {
		if info.Langs[i].Lines != info.Langs[j].Lines {
			return info.Langs[i].Lines > info.Langs[j].Lines
		}
		return info.Langs[i].Name < info.Langs[j].Name
	})
	// A project of many languages lists the eight with the most lines, and
	// folds the rest (and any existing Other) into one Other row at the end.
	const kept = 8
	if len(info.Langs) > kept {
		var top []projectLang
		other := projectInfoOther
		for _, l := range info.Langs {
			if l.Name == projectInfoOther.Name || len(top) >= kept {
				other.Files += l.Files
				other.Lines += l.Lines
			} else {
				top = append(top, l)
			}
		}
		if other.Lines > 0 {
			top = append(top, other)
		}
		info.Langs = top
	}
}

// inspectProjectFile reads one file: its size, the lines in it, and the
// language it is, for a text file small enough to count. A binary file,
// or one past projectInfoMaxLines, is sized and left at that.
func inspectProjectFile(path string) (projectFile, bool) {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return projectFile{}, false
	}
	st := projectFile{size: fi.Size()}
	if fi.Size() > projectInfoMaxLines {
		return st, true
	}
	f, err := os.Open(path)
	if err != nil {
		return st, true
	}
	defer f.Close()
	head := make([]byte, min(fi.Size(), projectInfoHead))
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return st, true
	}
	head = head[:n]
	if bytes.IndexByte(head, 0) >= 0 {
		return st, true // a binary file is sized, not counted
	}
	text := head
	if int64(n) < fi.Size() {
		rest, err := io.ReadAll(f)
		if err != nil {
			return st, true
		}
		text = append(head, rest...)
	}
	st.lines, st.nonBlank = countTextLines(text)
	lang := projectLanguageOf(path)
	st.lang = &lang
	return st, true
}

// countTextLines counts the lines of text, a last one that has no newline
// after it with them, and those of them that are not blank.
func countTextLines(text []byte) (lines, nonBlank int) {
	if len(text) == 0 {
		return 0, 0
	}
	for _, line := range bytes.Split(text, []byte{'\n'}) {
		lines++
		if len(bytes.TrimSpace(line)) > 0 {
			nonBlank++
		}
	}
	if text[len(text)-1] == '\n' {
		lines-- // bytes.Split ends in an empty line after the last newline
	}
	return lines, nonBlank
}

// projectLanguageOf names the language a file's name or extension reads
// as, Other for a text file of no known one.
func projectLanguageOf(path string) projectLang {
	name := strings.ToLower(filepath.Base(path))
	for _, known := range []string{"dockerfile", "makefile", "cmakelists.txt"} {
		if name == known {
			if known == "cmakelists.txt" {
				return projectLanguages["cpp"]
			}
			return projectLanguages[known]
		}
	}
	ext := strings.TrimPrefix(filepath.Ext(name), ".")
	if l, ok := projectLanguages[ext]; ok {
		return l
	}
	return projectInfoOther
}

// gitRead runs git in dir and returns what it wrote, one line ended by a
// newline, without asking for a password nor opening an editor.
func gitRead(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C", "GIT_EDITOR=true")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// readGitInfo reads the repository's branch, remote, commits, contributors
// and dates. A repository without a commit yet is read as one, with no
// commit to name.
func readGitInfo(info *projectInfo, root string) {
	if out, err := gitRead(root, "symbolic-ref", "--short", "-q", "HEAD"); err == nil && out != "" {
		info.Branch = out
	} else if out, err := gitRead(root, "rev-parse", "--short", "HEAD"); err == nil {
		info.Branch = out
	}
	if names, err := gitRead(root, "remote"); err == nil && names != "" {
		name := strings.Split(names, "\n")[0]
		for _, n := range strings.Split(names, "\n") {
			if n == "origin" {
				name = n
				break
			}
		}
		if url, err := gitRead(root, "remote", "get-url", name); err == nil && url != "" {
			info.Remote = shortRemoteURL(url)
		}
	}
	if out, err := gitRead(root, "for-each-ref", "--format=%(refname)", "refs/heads"); err == nil && out != "" {
		info.Branches = len(strings.Split(out, "\n"))
	}
	if out, err := gitRead(root, "rev-list", "--count", "HEAD"); err == nil {
		info.Commits, _ = strconv.Atoi(strings.TrimSpace(out))
	}
	if out, err := gitRead(root, "shortlog", "-sn", "HEAD"); err == nil && out != "" {
		info.Contributors = len(strings.Split(out, "\n"))
	}
	if out, err := gitRead(root, "log", "-1", "--format=%ct", "HEAD"); err == nil {
		if s, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64); err == nil {
			info.Last = time.Unix(s, 0)
		}
	}
	if roots, err := gitRead(root, "rev-list", "--max-parents=0", "HEAD"); err == nil && roots != "" {
		for _, hash := range strings.Split(roots, "\n") {
			out, err := gitRead(root, "show", "-s", "--format=%ct", hash)
			if err != nil {
				continue
			}
			s, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
			if err != nil {
				continue
			}
			t := time.Unix(s, 0)
			if info.First.IsZero() || t.Before(info.First) {
				info.First = t
			}
		}
	}
	if out, err := gitRead(root, "status", "--porcelain"); err == nil && out != "" {
		info.Changed = len(strings.Split(out, "\n"))
	}
	now := time.Now()
	if sr := readOneStat(root, nil, now); sr != nil {
		for _, cm := range sr.Commits {
			for s := 0; s < statSpans; s++ {
				if cm.When.After(now.Add(-statSpanLong[s])) {
					info.Recent[s].Commits++
					info.Recent[s].Added += cm.Added
					info.Recent[s].Removed += cm.Removed
				}
			}
		}
		for s := 0; s < statSpans; s++ {
			info.Recent[s].Lines = true
		}
	}
}

// projectInfoState is the dialog of a project's files and lines: whether
// it is open, what it shows, and while it reads.
type projectInfoState struct {
	open bool
	dir  string
	name string
	gen  int
	busy bool
	data *projectInfo
	err  string
}

// openProjectInfo opens the dialog on dir and reads it, leaving the answer
// of a read that an opened dialog replaced aside.
func (a *app) openProjectInfo(dir, name string) {
	if dir == "" || remote.Is(dir) {
		return
	}
	if name == "" {
		name = filepath.Base(dir)
	}
	pi := &a.projectInfo
	pi.open, pi.dir, pi.name = true, dir, name
	pi.read(a)
}

// read starts the scan, and shows it when it answers: a folder of
// thousands of files reads for a moment, and the window must not wait.
func (st *projectInfoState) read(a *app) {
	if !st.open || st.dir == "" || a == nil {
		return
	}
	gen := st.gen + 1
	st.gen, st.busy, st.data, st.err = gen, true, nil, ""
	a.invalidate()
	dir, name := st.dir, st.name
	bg.Go(func() {
		data, err := scanProjectInfo(dir)
		a.post(func() {
			if st.gen != gen || !st.open {
				return
			}
			st.busy = false
			if err != nil {
				st.err = err.Error()
			} else {
				data.Name = name
				st.data = data
			}
			a.invalidate()
		})
	})
}

// projectInfoView is the dialog of a project's files, lines, languages
// and repository: what it is at a glance, and what its Git holds.
func (a *app) projectInfoView(c *ui.Context) {
	pi := &a.projectInfo
	if !pi.open {
		return
	}
	p := a.pal
	ui.Overlay(c, func() {
		back := a.dialogBack(c)
		back.Children(func() {
			box := a.floating(c, ui.Column(c).Width(540).MaxWidthPercent(92).Margin(titleBarH+8, 0, 0, 0).Radius(10), true).Clip().
				Role(ui.RoleDialog).Label(locales.ProjectInfoTitle())
			box.Children(func() {
				a.projectInfoHead(c)
				// What the scan found, or why it found nothing.
				ui.Column(c).Padding(14, 16).Gap(12).MinHeight(120).Children(func() {
					switch {
					case pi.busy && pi.data == nil:
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, locales.ProjectInfoReading()).FontSize(12.5).TextColor(p.faint).SingleLine()
						})
					case pi.err != "":
						ui.Text(c, locales.ProjectInfoFailed(pi.err)).FontSize(12.5).TextColor(p.dangerFg)
					case pi.data != nil:
						a.projectInfoBody(c, pi.data)
					}
				})
			})
			if box.PressedOutside() || back.OverlayShortcut(0, ui.KeyEscape) {
				pi.open = false
			}
		})
	})
}

// projectInfoHead is the dialog's top: its icon and the project's name,
// the folder under it, and the buttons that read again and close it.
func (a *app) projectInfoHead(c *ui.Context) {
	p, pi := a.pal, &a.projectInfo
	ui.Row(c).Padding(11, 14).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
		ui.Icon(c, iconInfo).FontSize(14).TextColor(p.muted)
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, pi.name).FontSize(13.5).TextColor(p.text).SingleLine()
			path := ui.ButtonBase(c.Key("project-info-path")).Height(16).Padding(0).Gap(0).Radius(3).
				Label(pi.dir).Tooltip(locales.ProjectInfoCopyPath()).MinWidth(0).Shrink(1)
			if path.Hovered() {
				path.Background(p.hover)
			}
			path.Transition(hoverFade)
			path.Children(func() {
				ui.Text(c, shortenHome(pi.dir)).FontSize(11).TextColor(p.faint).SingleLine().MinWidth(0)
			})
			if path.Clicked() {
				a.services.WriteClipboard(hostPath(pi.dir))
				a.tell(note{message: locales.ProjectInfoCopied()})
			}
		})
		if a.smallButton(c, iconRefresh, locales.ProjectInfoRefresh()) {
			pi.read(a)
		}
		if a.smallButton(c, iconX, locales.QuickClose()) {
			pi.open = false
		}
	})
}

// projectInfoBody is what the dialog shows of a project read: its four
// numbers, the languages its lines are in, and its Git repository.
func (a *app) projectInfoBody(c *ui.Context, info *projectInfo) {
	p := a.pal
	a.projectInfoMetrics(c, info)
	if info.IsGit {
		a.projectInfoRecent(c, info)
	}
	if len(info.Langs) > 0 {
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, locales.ProjectInfoLanguages()).FontSize(11.5).TextColor(p.faint).SingleLine()
			a.projectInfoBar(c, info)
			ui.Scroll(c.Key("project-info-langs")).MaxHeight(200).Children(func() {
				for _, l := range info.Langs {
					a.projectInfoLang(c, l, info.Lines)
				}
			})
		})
	}
	if info.IsGit {
		a.projectInfoRepo(c, info)
	}
}

// projectInfoRecent shows the repository's 24h, 7d and 30d commit and line
// changes in three columns matching the Recent Commits panel.
func (a *app) projectInfoRecent(c *ui.Context, info *projectInfo) {
	p := a.pal
	ui.Column(c).Gap(6).Padding(10, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(p.border).Children(func() {
		ui.Text(c, locales.StatsTitle()).FontSize(11.5).TextColor(p.faint).SingleLine()
		ui.Row(c).Gap(10).Children(func() {
			for s := 0; s < statSpans; s++ {
				cnt := info.Recent[s]
				ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(3).Children(func() {
					ui.Text(c, statSpanName(s)).FontSize(10.5).TextColor(p.faint).SingleLine()
					col := p.text
					if cnt.Commits == 0 {
						col = p.faint
					}
					ui.Text(c, locales.StatsCommits(cnt.Commits)).FontSize(12.5).FontFeatures("tnum").TextColor(col).SingleLine()
					a.statsLines(c, cnt)
				})
			}
		})
	})
}

// projectInfoMetrics is the four numbers of a project, one under each of
// four labels, in tabular figures so that they line up.
func (a *app) projectInfoMetrics(c *ui.Context, info *projectInfo) {
	p := a.pal
	commits := "\u2014"
	if info.IsGit {
		commits = groupDigits(info.Commits)
	}
	ui.Row(c).Gap(10).Children(func() {
		for _, m := range []struct{ label, value string }{
			{locales.ProjectInfoLines(), groupDigits(info.Lines)},
			{locales.ProjectInfoFiles(), groupDigits(info.Files)},
			{locales.ProjectInfoSize(), byteSize(info.Size)},
			{locales.ProjectInfoCommits(), commits},
		} {
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(3).Children(func() {
				ui.Text(c, m.label).FontSize(10.5).TextColor(p.faint).SingleLine()
				ui.Text(c, m.value).FontSize(15).FontFeatures("tnum").TextColor(p.text).SingleLine()
			})
		}
	})
}

// projectInfoBar is the bar of the languages of a project: each one a
// segment as wide as its share of the lines.
func (a *app) projectInfoBar(c *ui.Context, info *projectInfo) {
	p := a.pal
	ui.Box(c).FillWidth().Height(8).Radius(4).Clip().Margin(0, 0, 4, 0).Draw(func(pt *ui.Painter, r ui.Rect) {
		if info.Lines <= 0 || r.W <= 0 {
			pt.Fill(r, p.border, 0)
			return
		}
		x := r.X
		for i, l := range info.Langs {
			w := r.W * float32(l.Lines) / float32(info.Lines)
			if i == len(info.Langs)-1 {
				w = r.X + r.W - x // the last takes what rounding left
			}
			pt.Fill(ui.Rect{X: x, Y: r.Y, W: w, H: r.H}, ui.Hex(l.Color), 0)
			x += w
		}
	})
}

// projectInfoLang is one language of a project: its color, name, files,
// lines and share of the lines.
func (a *app) projectInfoLang(c *ui.Context, l projectLang, total int) {
	p := a.pal
	share := float32(0)
	if total > 0 {
		share = float32(l.Lines) * 100 / float32(total)
	}
	ui.Row(c).Height(26).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(8, 8).Radius(4).Background(ui.Hex(l.Color)).Shrink(0)
		ui.Text(c, l.Name).FontSize(12.5).TextColor(p.text).Grow(1).Basis(0).MinWidth(0).SingleLine()
		ui.Text(c, groupDigits(l.Files)).Width(56).Shrink(0).FontSize(11.5).FontFeatures("tnum").
			TextColor(p.faint).TextAlign(ui.End).SingleLine()
		ui.Text(c, groupDigits(l.Lines)).Width(72).Shrink(0).FontSize(11.5).FontFeatures("tnum").
			TextColor(p.muted).TextAlign(ui.End).SingleLine()
		ui.Text(c, strconv.FormatFloat(float64(share), 'f', 1, 64)+"%").Width(52).Shrink(0).FontSize(11.5).
			FontFeatures("tnum").TextColor(p.faint).TextAlign(ui.End).SingleLine()
	})
}

// projectInfoRepo is the Git repository of a project, as a grid of what it
// holds: its branch, remote, branches, contributors and dates, and its
// working tree.
func (a *app) projectInfoRepo(c *ui.Context, info *projectInfo) {
	p := a.pal
	rows := []struct{ label, value string }{
		{locales.ProjectInfoBranch(), info.Branch},
	}
	if info.Remote != "" {
		rows = append(rows, struct{ label, value string }{locales.ProjectInfoRemote(), info.Remote})
	}
	rows = append(rows,
		struct{ label, value string }{locales.ProjectInfoBranches(), groupDigits(info.Branches)},
		struct{ label, value string }{locales.ProjectInfoContributors(), groupDigits(info.Contributors)},
		struct{ label, value string }{locales.ProjectInfoFirstCommit(), infoDate(info.First)},
		struct{ label, value string }{locales.ProjectInfoLastCommit(), infoDate(info.Last)},
	)
	tree := locales.ProjectInfoClean()
	if info.Changed > 0 {
		tree = locales.ProjectInfoChanged(info.Changed)
	}
	rows = append(rows, struct{ label, value string }{locales.ProjectInfoWorkingTree(), tree})
	ui.Column(c).Gap(7).Children(func() {
		ui.Text(c, locales.ProjectInfoRepository()).FontSize(11.5).TextColor(p.faint).SingleLine()
		for _, r := range rows {
			value := r.value
			if value == "" {
				value = "\u2014"
			}
			ui.Row(c).Gap(10).Children(func() {
				ui.Text(c, r.label).Width(104).Shrink(0).FontSize(12).TextColor(p.faint).SingleLine()
				ui.Text(c, value).FontSize(12).FontFeatures("tnum").TextColor(p.text).Grow(1).Basis(0).MinWidth(0).SingleLine()
			})
		}
	})
}

// infoDate is when a commit was made, as the dialog says it: a day, and
// its year where it is another's.
func infoDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	if t.Year() != time.Now().Year() {
		return t.Format("Jan 2, 2006")
	}
	return t.Format("Jan 2")
}

// groupDigits writes a count with its thousands set apart, as 128,450.
func groupDigits(n int) string {
	s := strconv.Itoa(n)
	if n < 1000 {
		return s
	}
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
